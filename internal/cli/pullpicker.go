// Package cli – interactive fuzzy-search picker used by "notx pull".
//
// golang.org/x/term is NOT available as a compiled source dependency (only its
// go.mod hash appears in go.sum), so raw mode and terminal-size queries are
// implemented directly via syscall ioctl on darwin and linux.  No new external
// dependencies are added.
package cli

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

// ─────────────────────────────────────────────────────────────────────────────
// ANSI escape constants
// ─────────────────────────────────────────────────────────────────────────────

const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiCyan    = "\033[36m"
	ansiGreen   = "\033[32m"
	ansiClear   = "\033[H\033[2J" // cursor home + erase display
	ansiErasEOL = "\033[K"        // erase from cursor to end of line
	ansiHide    = "\033[?25l"
	ansiShow    = "\033[?25h"
)

// ─────────────────────────────────────────────────────────────────────────────
// Public types
// ─────────────────────────────────────────────────────────────────────────────

// PickerItem is one row in the picker list.
type PickerItem struct {
	URN         string // full note URN
	Name        string // note name
	ShortURN    string // first 8 chars of the UUID portion + "…"
	Description string // "ProjectName / FolderName" or "" if unset
	Shortcut    string // shortcut alias if one is registered, "" otherwise
}

// RunPicker launches the interactive picker and returns the selected item.
// Returns (item, true) on selection, (PickerItem{}, false) if the user pressed
// Esc or Ctrl-C, or if any terminal setup error occurs.
func RunPicker(items []PickerItem) (PickerItem, bool) {
	// Open /dev/tty directly so the picker works even when stdin is redirected
	// (e.g. "notx pull --stdout | grep …").
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return PickerItem{}, false
	}
	defer tty.Close()

	// Enter raw mode; restore on exit regardless of how we leave.
	savedState, err := rawModeEnter(tty.Fd())
	if err != nil {
		return PickerItem{}, false
	}
	defer rawModeExit(tty.Fd(), savedState)

	// Hide the cursor while rendering.
	fmt.Fprint(os.Stderr, ansiHide)
	defer fmt.Fprint(os.Stderr, ansiShow)

	p := &pickerState{
		items:    items,
		filtered: make([]PickerItem, 0, len(items)),
	}
	p.refilter()

	for {
		p.render(tty.Fd())

		b, seq, err := readKey(tty)
		if err != nil {
			return PickerItem{}, false
		}

		switch {
		case seq == "up":
			if p.cursor > 0 {
				p.cursor--
			}

		case seq == "down":
			if p.cursor < len(p.filtered)-1 {
				p.cursor++
			}

		case b == 13 || b == 10: // Enter (CR or LF)
			if len(p.filtered) == 0 {
				continue
			}
			selected := p.filtered[p.cursor]
			fmt.Fprint(os.Stderr, ansiClear)
			return selected, true

		case b == 27 || b == 3: // Esc or Ctrl-C
			fmt.Fprint(os.Stderr, ansiClear)
			return PickerItem{}, false

		case b == 127 || b == 8: // Backspace / DEL
			if len(p.query) > 0 {
				_, size := utf8.DecodeLastRuneInString(p.query)
				p.query = p.query[:len(p.query)-size]
				p.refilter()
			}

		case b >= 32 && seq == "": // Printable ASCII
			p.query += string(rune(b))
			p.refilter()
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal picker state
// ─────────────────────────────────────────────────────────────────────────────

type pickerState struct {
	items    []PickerItem
	filtered []PickerItem
	query    string
	cursor   int
}

// refilter rebuilds the filtered slice from the current query and clamps cursor
// into the valid range.
func (p *pickerState) refilter() {
	q := strings.ToLower(p.query)
	p.filtered = p.filtered[:0]

	if q == "" {
		p.filtered = append(p.filtered, p.items...)
	} else {
		for _, item := range p.items {
			if strings.Contains(strings.ToLower(item.Name), q) ||
				strings.Contains(strings.ToLower(item.Description), q) ||
				strings.Contains(strings.ToLower(item.Shortcut), q) {
				p.filtered = append(p.filtered, item)
			}
		}
	}

	switch {
	case len(p.filtered) == 0:
		p.cursor = 0
	case p.cursor >= len(p.filtered):
		p.cursor = len(p.filtered) - 1
	}
}

// render writes the complete picker UI to stderr as a single buffered write.
// fd is used only to query the terminal width.
func (p *pickerState) render(fd uintptr) {
	cols := termWidth(fd)
	if cols < 40 {
		cols = 40 // sane minimum so we always show something
	}

	var buf bytes.Buffer

	// In raw mode \n does not return to column 0 — we must emit \r\n.
	// We write every line via writeLine() which appends \033[K\r\n.

	// Clear screen, move cursor to top-left.
	buf.WriteString(ansiClear)

	// ── Search prompt ────────────────────────────────────────────────────────
	writeLine(&buf, "", cols) // blank leading line
	searchLine := fmt.Sprintf("  %sSearch:%s %s%s%s█",
		ansiBold, ansiReset, ansiCyan, p.query, ansiReset)
	writeLine(&buf, searchLine, cols)
	writeLine(&buf, "", cols) // blank separator

	// ── Item rows ────────────────────────────────────────────────────────────
	if len(p.filtered) == 0 {
		writeLine(&buf, fmt.Sprintf("  %s(no matches)%s", ansiDim, ansiReset), cols)
	} else {
		// Compute column widths that fit inside cols.
		//
		// Layout (all measurements in visible runes, not bytes):
		//   prefix    : 4  ("  > " or "    ")
		//   name      : clamped to [8, 28]
		//   gap       : 2
		//   shortURN  : 10  ("xxxxxxxx…")
		//   gap       : 2
		//   desc      : whatever remains, max 32, hidden if < 6
		//   gap+sc    : optional shortcut "· alias" appended if it fits
		//
		// We compute nameW first from the actual data, then fit the rest.

		nameW := longestNameWidth(p.filtered) // already clamped to 28
		if nameW < 8 {
			nameW = 8
		}

		const (
			prefixW   = 4
			shortURNW = 10
			gapW      = 2
		)

		// Available width for desc after mandatory columns.
		// mandatory = prefix + name + gap + shortURN + gap
		mandatory := prefixW + nameW + gapW + shortURNW + gapW
		descAvail := cols - mandatory - 2 // -2 for right margin
		if descAvail > 32 {
			descAvail = 32
		}
		showDesc := descAvail >= 6

		for i, item := range p.filtered {
			writePickerRow(&buf, i == p.cursor, item, nameW, shortURNW, descAvail, showDesc, cols)
		}
	}

	// ── Status line ──────────────────────────────────────────────────────────
	writeLine(&buf, "", cols) // blank separator
	status := fmt.Sprintf("  %s%d/%d notes  ↑↓ navigate  enter select  esc cancel%s",
		ansiDim, len(p.filtered), len(p.items), ansiReset)
	writeLine(&buf, status, cols)

	_, _ = os.Stderr.Write(buf.Bytes())
}

// writeLine appends line + erase-to-EOL + \r\n to buf.
// In raw mode \r\n is required to move the cursor to column 0 of the next line.
// The erase-to-EOL clears any characters left over from a previous render.
func writeLine(buf *bytes.Buffer, line string, _ int) {
	buf.WriteString(line)
	buf.WriteString(ansiErasEOL)
	buf.WriteString("\r\n")
}

// writePickerRow formats a single item row and appends it to buf.
func writePickerRow(
	buf *bytes.Buffer,
	selected bool,
	item PickerItem,
	nameW, shortURNW, descAvail int,
	showDesc bool,
	cols int,
) {
	// Build the visible content of the line into a separate buffer so we can
	// measure its display width before deciding whether to append the shortcut.
	var line strings.Builder

	if selected {
		line.WriteString(ansiBold)
		line.WriteString(ansiGreen)
		line.WriteString("> ")
		line.WriteString(ansiReset)
		line.WriteString(ansiBold)
	} else {
		line.WriteString("  ")
	}

	// Two-space indent inside prefix (prefix = "  > " or "    ").
	line.WriteString("  ")

	// Name column.
	name := visiblePadRight(item.Name, nameW)
	line.WriteString(name)

	// Short URN column.
	line.WriteString("  ")
	if selected {
		line.WriteString(ansiDim + "·" + ansiReset + ansiBold)
	} else {
		line.WriteString(ansiDim + "·" + ansiReset)
	}
	line.WriteString(" ")
	line.WriteString(visiblePadRight(item.ShortURN, shortURNW))

	// Description column (optional).
	if showDesc {
		line.WriteString("  ")
		if selected {
			line.WriteString(ansiDim + "·" + ansiReset + ansiBold)
		} else {
			line.WriteString(ansiDim + "·" + ansiReset)
		}
		line.WriteString(" ")
		desc := visibleTruncate(item.Description, descAvail)
		desc = visiblePadRight(desc, descAvail)
		line.WriteString(desc)
	}

	// Shortcut column — only append if it fits within cols.
	if item.Shortcut != "" {
		sc := "  " + ansiDim + "·" + ansiReset
		if selected {
			sc += ansiBold
		}
		sc += " " + ansiCyan + item.Shortcut + ansiReset

		// Measure visible width of the whole line so far + shortcut addition.
		// We compare raw visible widths (strip ANSI for measurement).
		currentVis := visibleLen(line.String())
		addVis := visibleLen(sc)
		if currentVis+addVis+2 <= cols {
			line.WriteString(sc)
		}
	}

	if selected {
		line.WriteString(ansiReset)
	}

	writeLine(buf, line.String(), cols)
}

// ─────────────────────────────────────────────────────────────────────────────
// Visible-width helpers  (ANSI-escape-aware)
// ─────────────────────────────────────────────────────────────────────────────

// visibleLen returns the number of printable runes in s, skipping ANSI CSI
// escape sequences of the form ESC [ ... <final-byte>.
func visibleLen(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			// Final byte of a CSI sequence is in range 0x40–0x7E.
			if r >= 0x40 && r <= 0x7E {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			continue
		}
		n++
	}
	return n
}

// visiblePadRight pads s (by visible width) with spaces to width runes.
// If s is already wider than width it is truncated via visibleTruncate.
func visiblePadRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	vw := visibleLen(s)
	if vw >= width {
		return visibleTruncate(s, width)
	}
	return s + strings.Repeat(" ", width-vw)
}

// visibleTruncate shortens s to at most maxVis visible runes, appending "…"
// when the string is cut.  ANSI escape sequences do not count toward the limit
// and are preserved (best-effort — a sequence straddling the cut is dropped).
func visibleTruncate(s string, maxVis int) string {
	if maxVis <= 0 {
		return ""
	}
	// Fast path: already fits.
	if visibleLen(s) <= maxVis {
		return s
	}

	var out strings.Builder
	n := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			out.WriteRune(r)
			if r >= 0x40 && r <= 0x7E {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			out.WriteRune(r)
			continue
		}
		if n == maxVis-1 {
			// One slot left — write the ellipsis and stop.
			out.WriteRune('…')
			break
		}
		out.WriteRune(r)
		n++
	}
	return out.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Terminal width
// ─────────────────────────────────────────────────────────────────────────────

// termWidth queries the terminal width via TIOCGWINSZ.  Falls back to 80 on
// any error.
func termWidth(fd uintptr) int {
	type winsize struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}
	var ws winsize
	var req uintptr
	switch runtime.GOOS {
	case "darwin":
		req = 0x40087468 // TIOCGWINSZ on macOS
	case "linux":
		req = 0x5413 // TIOCGWINSZ on Linux
	default:
		return 80
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.Col == 0 {
		return 80
	}
	return int(ws.Col)
}

// ─────────────────────────────────────────────────────────────────────────────
// Column helpers
// ─────────────────────────────────────────────────────────────────────────────

// longestNameWidth returns the name-column display width for the current
// filtered list, clamped to [0, 28].
func longestNameWidth(items []PickerItem) int {
	maxW := 0
	for _, it := range items {
		if w := utf8.RuneCountInString(it.Name); w > maxW {
			maxW = w
		}
	}
	if maxW > 28 {
		return 28
	}
	return maxW
}

// ─────────────────────────────────────────────────────────────────────────────
// Key reading
// ─────────────────────────────────────────────────────────────────────────────

// readKey reads one logical key press from tty.
// It returns (byteVal, seqName, error).
// seqName is "up" or "down" for the corresponding arrow keys; "" for all other
// keys (in which case byteVal is the relevant byte).
func readKey(tty *os.File) (byte, string, error) {
	one := make([]byte, 1)
	if _, err := tty.Read(one); err != nil {
		return 0, "", err
	}
	b := one[0]

	// Not an escape — return the byte directly.
	if b != 0x1b {
		return b, "", nil
	}

	// Potential CSI escape sequence: ESC [ X
	// Because we are in raw mode, the bytes arrive immediately if they were
	// sent as part of a single escape sequence by the terminal.
	bracket := make([]byte, 1)
	n, _ := tty.Read(bracket)
	if n == 0 || bracket[0] != '[' {
		// Bare ESC (or unrecognised two-byte sequence) → treat as ESC.
		return 0x1b, "", nil
	}

	final := make([]byte, 1)
	n, _ = tty.Read(final)
	if n == 0 {
		return 0x1b, "", nil
	}

	switch final[0] {
	case 'A':
		return 0, "up", nil
	case 'B':
		return 0, "down", nil
	}

	// Any other CSI sequence we don't handle → treat as ESC so the loop stays
	// safe (e.g. ESC will cancel, which is better than doing nothing visible).
	return 0x1b, "", nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Raw-mode via syscall ioctl  (darwin + linux, amd64 / arm64)
// ─────────────────────────────────────────────────────────────────────────────
//
// We cannot use golang.org/x/term because only its go.mod hash appears in
// go.sum (the source has not been fetched as a real dependency).
//
// The two structs below mirror the C termios layout on each platform.

// darwinTermios mirrors <sys/termios.h> on macOS (amd64 and arm64).
type darwinTermios struct {
	Iflag  uint64
	Oflag  uint64
	Cflag  uint64
	Lflag  uint64
	Cc     [20]uint8
	Ispeed uint64
	Ospeed uint64
}

// linuxTermios mirrors struct termios on Linux (amd64 and arm64).
type linuxTermios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Line   uint8
	Cc     [19]uint8
	Ispeed uint32
	Ospeed uint32
}

// ioctl request numbers.
const (
	darwinTIOCGETA uintptr = 0x40487413
	darwinTIOCSETA uintptr = 0x80487414

	linuxTCGETS uintptr = 0x5401
	linuxTCSETS uintptr = 0x5402
)

// rawModeEnter saves the current terminal attributes for fd and puts the
// terminal into raw mode.  It returns an opaque byte slice that rawModeExit
// uses to restore the original state.
func rawModeEnter(fd uintptr) ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return darwinRawEnter(fd)
	case "linux":
		return linuxRawEnter(fd)
	default:
		return nil, fmt.Errorf("pullpicker: unsupported OS %q", runtime.GOOS)
	}
}

// rawModeExit restores the terminal attributes previously saved by rawModeEnter.
func rawModeExit(fd uintptr, saved []byte) {
	if len(saved) == 0 {
		return
	}
	switch runtime.GOOS {
	case "darwin":
		darwinRawExit(fd, saved)
	case "linux":
		linuxRawExit(fd, saved)
	}
}

// ── darwin ───────────────────────────────────────────────────────────────────

func darwinRawEnter(fd uintptr) ([]byte, error) {
	var t darwinTermios
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL, fd, darwinTIOCGETA,
		uintptr(unsafe.Pointer(&t)),
	); errno != 0 {
		return nil, errno
	}

	saved := darwinTermiosBytes(t)

	// cfmakeraw equivalent:
	t.Iflag &^= uint64(syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK |
		syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON)
	t.Oflag &^= uint64(syscall.OPOST)
	t.Lflag &^= uint64(syscall.ECHO | syscall.ECHONL | syscall.ICANON |
		syscall.ISIG | syscall.IEXTEN)
	t.Cflag &^= uint64(syscall.CSIZE | syscall.PARENB)
	t.Cflag |= uint64(syscall.CS8)
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0

	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL, fd, darwinTIOCSETA,
		uintptr(unsafe.Pointer(&t)),
	); errno != 0 {
		return nil, errno
	}
	return saved, nil
}

func darwinRawExit(fd uintptr, saved []byte) {
	t := darwinTermiosFromBytes(saved)
	_, _, _ = syscall.Syscall(
		syscall.SYS_IOCTL, fd, darwinTIOCSETA,
		uintptr(unsafe.Pointer(&t)),
	)
}

// darwinTermiosBytes serialises t into a byte slice.
func darwinTermiosBytes(t darwinTermios) []byte {
	const sz = unsafe.Sizeof(darwinTermios{})
	b := make([]byte, sz)
	copy(b, (*[unsafe.Sizeof(darwinTermios{})]byte)(unsafe.Pointer(&t))[:])
	return b
}

// darwinTermiosFromBytes deserialises a byte slice into a darwinTermios.
func darwinTermiosFromBytes(b []byte) darwinTermios {
	var t darwinTermios
	copy((*[unsafe.Sizeof(darwinTermios{})]byte)(unsafe.Pointer(&t))[:], b)
	return t
}

// ── linux ────────────────────────────────────────────────────────────────────

func linuxRawEnter(fd uintptr) ([]byte, error) {
	var t linuxTermios
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL, fd, linuxTCGETS,
		uintptr(unsafe.Pointer(&t)),
	); errno != 0 {
		return nil, errno
	}

	saved := linuxTermiosBytes(t)

	// cfmakeraw equivalent:
	t.Iflag &^= uint32(syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK |
		syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON)
	t.Oflag &^= uint32(syscall.OPOST)
	t.Lflag &^= uint32(syscall.ECHO | syscall.ECHONL | syscall.ICANON |
		syscall.ISIG | syscall.IEXTEN)
	t.Cflag &^= uint32(syscall.CSIZE | syscall.PARENB)
	t.Cflag |= uint32(syscall.CS8)
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0

	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL, fd, linuxTCSETS,
		uintptr(unsafe.Pointer(&t)),
	); errno != 0 {
		return nil, errno
	}
	return saved, nil
}

func linuxRawExit(fd uintptr, saved []byte) {
	t := linuxTermiosFromBytes(saved)
	_, _, _ = syscall.Syscall(
		syscall.SYS_IOCTL, fd, linuxTCSETS,
		uintptr(unsafe.Pointer(&t)),
	)
}

// linuxTermiosBytes serialises t into a byte slice.
func linuxTermiosBytes(t linuxTermios) []byte {
	b := make([]byte, unsafe.Sizeof(linuxTermios{}))
	copy(b, (*[unsafe.Sizeof(linuxTermios{})]byte)(unsafe.Pointer(&t))[:])
	return b
}

// linuxTermiosFromBytes deserialises a byte slice into a linuxTermios.
func linuxTermiosFromBytes(b []byte) linuxTermios {
	var t linuxTermios
	copy((*[unsafe.Sizeof(linuxTermios{})]byte)(unsafe.Pointer(&t))[:], b)
	return t
}
