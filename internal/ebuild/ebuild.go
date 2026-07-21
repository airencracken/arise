package ebuild

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Ebuild struct {
	EAPI          string
	Inherit       []string
	Variables     map[string]string
	RawPhases     map[string]string
	RawPhaseOrder []string
	FilePath      string
}

var defaultPhaseOrder = []string{
	"pkg_pretend", "pkg_setup",
	"src_unpack", "src_prepare", "src_configure",
	"src_compile", "src_test", "src_install",
	"pkg_preinst", "pkg_postinst", "pkg_prerm", "pkg_postrm",
}

var knownPhases = map[string]bool{
	"pkg_pretend": true, "pkg_setup": true,
	"src_unpack": true, "src_prepare": true,
	"src_configure": true, "src_compile": true,
	"src_test": true, "src_install": true,
	"pkg_preinst": true, "pkg_postinst": true,
	"pkg_prerm": true, "pkg_postrm": true,
	"pkg_config": true, "pkg_info": true,
	"pkg_nofetch": true,
}

func ParseEbuild(path string) (eb *Ebuild, err error) {
	defer func() {
		if r := recover(); r != nil {
			eb = nil
			err = fmt.Errorf("ebuild: panic parsing %s: %v", path, r)
		}
	}()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ebuild: read %s: %w", path, err)
	}

	if bytes.Contains(data, []byte{0}) {
		return nil, fmt.Errorf("ebuild: %s contains null bytes", path)
	}

	logical := mergeContinuations(string(data))
	return parse(logical, path)
}

func mergeContinuations(raw string) []string {
	rawLines := strings.Split(raw, "\n")
	var out []string
	var buf strings.Builder

	for i := 0; i < len(rawLines); i++ {
		line := rawLines[i]
		trimmed := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmed, "\\") {
			buf.WriteString(strings.TrimSuffix(trimmed, "\\"))
			continue
		}
		if buf.Len() > 0 {
			buf.WriteString(line)
			out = append(out, buf.String())
			buf.Reset()
		} else {
			out = append(out, line)
		}
	}

	if buf.Len() > 0 {
		out = append(out, buf.String())
	}

	return out
}

type parser struct {
	lines          []string
	pos            int
	braceDepth     int
	condDepth      int
	inFunc         bool
	funcName       string
	funcLines      []string
	pendingFunc    string
	pendingVarName string
	pendingVarBuf  []string
	ebuild         *Ebuild
}

func parse(lines []string, path string) (*Ebuild, error) {
	e := &Ebuild{
		Variables:     make(map[string]string),
		RawPhases:     make(map[string]string),
		RawPhaseOrder: make([]string, len(defaultPhaseOrder)),
		FilePath:      path,
	}
	copy(e.RawPhaseOrder, defaultPhaseOrder)

	p := &parser{
		lines:  lines,
		ebuild: e,
	}

	for p.pos < len(p.lines) {
		p.processLine()
	}

	if p.braceDepth > 0 {
		return nil, fmt.Errorf("ebuild: %s: missing %d closing brace(s)", path, p.braceDepth)
	}
	if p.inFunc {
		return nil, fmt.Errorf("ebuild: %s: unclosed phase function %q", path, p.funcName)
	}

	if v, ok := e.Variables["EAPI"]; ok {
		e.EAPI = normalizeEAPI(v)
	} else if v, ok := e.Variables["eapi"]; ok {
		e.EAPI = normalizeEAPI(v)
	}

	return e, nil
}

func normalizeEAPI(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	return value
}

func (p *parser) processLine() {
	line := p.lines[p.pos]
	trimmed := strings.TrimSpace(line)
	p.pos++

	if p.pendingFunc != "" {
		p.handlePendingFunc(line, trimmed)
		return
	}

	if p.pendingVarName != "" {
		p.handlePendingVar(line, trimmed)
		return
	}

	if p.inFunc {
		p.processFuncLine(line)
		return
	}

	if p.condDepth > 0 {
		p.processCondLine(trimmed)
		return
	}

	if opens := countCondOpens(trimmed); opens > 0 {
		p.condDepth = opens
		closes := countCondCloses(trimmed)
		p.condDepth -= closes
		if p.condDepth < 0 {
			p.condDepth = 0
		}
		return
	}

	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return
	}

	if name, ok := isPhaseFuncHeader(trimmed); ok {
		if strings.Contains(trimmed[strings.Index(trimmed, "()")+2:], "{") {
			p.startFunction(name, line)
		} else {
			p.pendingFunc = name
		}
		return
	}

	if strings.HasPrefix(trimmed, "inherit ") {
		parts := strings.Fields(trimmed[len("inherit "):])
		p.ebuild.Inherit = append(p.ebuild.Inherit, parts...)
		return
	}

	name, value, more, ok := parseVarAssign(trimmed)
	if !ok {
		return
	}
	if more {
		p.pendingVarName = name
		p.pendingVarBuf = []string{value}
		return
	}
	p.ebuild.Variables[name] = value
}

func (p *parser) handlePendingVar(line, trimmed string) {
	p.pendingVarBuf = append(p.pendingVarBuf, line)
	combined := strings.Join(p.pendingVarBuf, "\n")
	if varComplete(combined) {
		p.ebuild.Variables[p.pendingVarName] = combined
		p.pendingVarName = ""
		p.pendingVarBuf = nil
	}
}

func varComplete(value string) bool {
	dq := 0
	sq := 0
	esc := false

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if esc {
			esc = false
			continue
		}
		if ch == '\\' {
			esc = true
			continue
		}
		switch ch {
		case '"':
			if sq == 0 {
				dq ^= 1
			}
		case '\'':
			if dq == 0 {
				sq ^= 1
			}
		}
	}

	return dq == 0 && sq == 0
}

func (p *parser) handlePendingFunc(line, trimmed string) {
	if strings.HasPrefix(trimmed, "{") {
		p.startFunction(p.pendingFunc, line)
		p.pendingFunc = ""
		return
	}
	p.pendingFunc = ""
	p.pos--
}

func (p *parser) startFunction(name, line string) {
	p.inFunc = true
	p.funcName = name
	p.funcLines = []string{line}
	p.braceDepth = countBracesNoComment(line)
	if p.braceDepth <= 0 {
		p.finishFunction()
	}
}

func (p *parser) processFuncLine(line string) {
	p.funcLines = append(p.funcLines, line)
	p.braceDepth += countBracesNoComment(line)
	if p.braceDepth <= 0 {
		p.finishFunction()
	}
}

func (p *parser) processCondLine(trimmed string) {
	p.condDepth += countCondOpens(trimmed) - countCondCloses(trimmed)
	if p.condDepth < 0 {
		p.condDepth = 0
	}
}

func (p *parser) finishFunction() {
	if p.braceDepth < 0 {
		p.braceDepth = 0
	}
	body := strings.Join(p.funcLines, "\n")
	p.ebuild.RawPhases[p.funcName] = body
	p.inFunc = false
	p.funcName = ""
	p.funcLines = nil
}

func countBracesNoComment(line string) int {
	line = stripShellComment(line)
	depth := 0
	var quote byte
	escaped := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return depth
}

// stripShellComment does not confuse parameter trimming (${value##pattern})
// or quoted hashes with shell comments.
func stripShellComment(line string) string {
	var quote byte
	escaped := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == '#' && (i == 0 || isShellCommentBoundary(line[i-1])) {
			return strings.TrimSpace(line[:i])
		}
	}
	return line
}

func isShellCommentBoundary(ch byte) bool {
	return ch == ' ' || ch == '\t' || strings.ContainsRune(";&|()<>", rune(ch))
}

func isPhaseFuncHeader(trimmed string) (string, bool) {
	parenIdx := strings.Index(trimmed, "()")
	if parenIdx < 0 {
		return "", false
	}
	name := strings.TrimSpace(trimmed[:parenIdx])
	if !knownPhases[name] {
		return "", false
	}
	return name, true
}

func parseVarAssign(trimmed string) (name, value string, more bool, ok bool) {
	hasExport := false
	hasLocal := false
	if after, found := strings.CutPrefix(trimmed, "export "); found {
		trimmed = strings.TrimSpace(after)
		hasExport = true
	} else if after, found := strings.CutPrefix(trimmed, "local "); found {
		trimmed = strings.TrimSpace(after)
		hasLocal = true
	}

	if idx := strings.IndexByte(trimmed, '='); idx > 0 {
		name = strings.TrimSpace(trimmed[:idx])
		value = strings.TrimSpace(trimmed[idx+1:])
		if name == "" || !isValidVarName(name) {
			return "", "", false, false
		}
		if !varComplete(value) {
			return name, value, true, true
		}
		return name, stripShellComment(value), false, true
	}

	// Handle export VAR / local VAR without assignment
	if (hasExport || hasLocal) && trimmed != "" && isValidVarName(trimmed) {
		return trimmed, "", false, true
	}

	return "", "", false, false
}

func isValidVarName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, r := range s {
		if i == 0 && !isAlphaOrUS(r) {
			return false
		}
		if !isAlphaNumOrUS(r) {
			return false
		}
	}
	return true
}

func isAlphaOrUS(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_'
}

func isAlphaNumOrUS(r rune) bool {
	return isAlphaOrUS(r) || (r >= '0' && r <= '9')
}

func countChar(s string, ch byte) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ch {
			n++
		}
	}
	return n
}

func countCondOpens(trimmed string) int {
	first := firstWord(trimmed)
	switch first {
	case "if", "for", "while", "until", "case":
		return 1
	}
	return 0
}

func countCondCloses(trimmed string) int {
	first := firstWord(trimmed)
	switch first {
	case "fi", "done", "esac":
		return 1
	}
	return 0
}

func firstWord(s string) string {
	if idx := strings.IndexAny(s, " \t;"); idx >= 0 {
		return s[:idx]
	}
	return s
}

func (e *Ebuild) Vars() map[string]string {
	base := filepath.Base(e.FilePath)
	name := strings.TrimSuffix(base, ".ebuild")
	pn, pv, pvr := parseFileName(name)
	p := pn
	if pv != "" {
		p = pn + "-" + pv
	}

	out := make(map[string]string, len(e.Variables))
	for k, v := range e.Variables {
		val := v
		val = strings.ReplaceAll(val, "${P}", p)
		val = strings.ReplaceAll(val, "${PN}", pn)
		val = strings.ReplaceAll(val, "${PV}", pv)
		val = strings.ReplaceAll(val, "${PVR}", pvr)
		val = strings.ReplaceAll(val, "$P", p)
		val = strings.ReplaceAll(val, "$PN", pn)
		val = strings.ReplaceAll(val, "$PV", pv)
		val = strings.ReplaceAll(val, "$PVR", pvr)
		out[k] = val
	}
	return out
}

func parseFileName(name string) (pn, pv, pvr string) {
	for i := len(name) - 1; i > 0; i-- {
		if name[i] == '-' && i+1 < len(name) && isDigit(name[i+1]) {
			pn = name[:i]
			pvr = name[i+1:]
			pv = pvr
			if idx := strings.LastIndex(pvr, "-r"); idx >= 0 {
				if rest := pvr[idx+2:]; rest != "" && isDigit(rest[0]) {
					pv = pvr[:idx]
				}
			}
			return
		}
	}
	return name, "", name
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func (e *Ebuild) SourceURIList() []string {
	vars := e.Vars()
	raw, ok := vars["SRC_URI"]
	if !ok {
		return nil
	}

	raw = stripQuotes(raw)
	fields := strings.Fields(raw)
	var uris []string
	skipNext := false
	for _, f := range fields {
		if f == "(" || f == ")" {
			continue
		}
		if strings.HasSuffix(f, "?") && !strings.Contains(f, ":") {
			continue
		}
		if skipNext {
			skipNext = false
			continue
		}
		if f == "->" {
			skipNext = true
			continue
		}
		if arrowIdx := strings.Index(f, "->"); arrowIdx >= 0 {
			f = strings.TrimSpace(f[:arrowIdx])
		}
		if f == "" {
			continue
		}
		uris = append(uris, f)
	}
	return uris
}

func stripQuotes(s string) string {
	for len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = s[1 : len(s)-1]
		} else {
			break
		}
	}
	return s
}
