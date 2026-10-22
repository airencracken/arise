package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/airencracken/arise/internal/color"
	"github.com/airencracken/arise/internal/portage"
	"golang.org/x/term"
)

type profileTextWriter struct {
	writer      io.Writer
	width       int
	root        string
	sourceWidth int
}

func writeProfileReport(writer io.Writer, report portage.ProfileReport, explain bool) {
	width := 100
	if stdoutIsTerminal() {
		if columns, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && columns > 0 {
			width = columns
		}
	}
	writeProfileReportWidth(writer, report, explain, width)
}

func writeProfileReportWidth(writer io.Writer, report portage.ProfileReport, explain bool, width int) {
	root := ""
	marker := string(filepath.Separator) + "profiles" + string(filepath.Separator)
	if index := strings.Index(report.ActivePath, marker); index >= 0 {
		root = report.ActivePath[:index+len(marker)-1]
	}
	output := profileTextWriter{writer: writer, width: max(24, width), root: root}
	output.sourceWidth = len("SOURCE")
	measureSources := func(changes []portage.ProfileContribution) {
		for _, change := range changes {
			output.sourceWidth = max(output.sourceWidth, utf8.RuneCountInString(output.source(change.Source)))
		}
	}
	for _, value := range report.Defaults {
		measureSources(value.Contributions)
	}
	for _, policy := range report.Policies {
		measureSources(policy.Contributions)
	}
	output.row("Profile", report.ActivePath, 2, 7, color.BoldCyan, color.Bold)
	output.row("Scope", "profile defaults and policy", 2, 7, color.Bold, nil)
	if root != "" {
		output.row("Root", root, 2, 7, color.Bold, color.Blue)
	}
	output.row("", "Local configuration, make.globals, command environment and package IUSE are excluded.", 2, 7, nil, nil)
	output.inheritance(report.Layers, report.ActivePath, explain)
	output.defaults(report.Defaults, explain)
	output.globalUse(report.Policies, explain)
	for _, policy := range report.Policies {
		if strings.HasPrefix(policy.Name, "use.") {
			continue
		}
		output.policy(policy, explain)
	}
	if len(report.Unsupported) != 0 {
		output.section("Files not evaluated", "outside supported report policy")
		for _, path := range report.Unsupported {
			output.row("", output.path(path), 2, 0, nil, color.Yellow)
		}
	}
}

func (output profileTextWriter) path(path string) string {
	if output.root != "" && strings.HasPrefix(path, output.root+string(filepath.Separator)) {
		return strings.TrimPrefix(path, output.root+string(filepath.Separator))
	}
	return path
}

func (output profileTextWriter) section(title, detail string) {
	fmt.Fprintf(output.writer, "\n%s", color.BoldCyan(title))
	if detail != "" {
		fmt.Fprintf(output.writer, "  (%s)", detail)
	}
	fmt.Fprintln(output.writer)
}

func (output profileTextWriter) inheritance(layers []portage.ProfileReportLayer, selected string, explain bool) {
	output.section("Inheritance (evaluation order)", fmt.Sprintf("%d layers", len(layers)))
	width := max(1, len(strconv.Itoa(len(layers))))
	output.row("#", "PROFILE", 2, width, color.Bold, color.Bold)
	for index, layer := range layers {
		name := output.path(layer.Path)
		style := color.Cyan
		if layer.Path == selected {
			name += "  [selected]"
			style = color.BoldGreen
		}
		output.row(strconv.Itoa(index+1), name, 2, width, color.Blue, style)
		if explain && len(layer.Parents) != 0 {
			output.row("", "parents: "+strings.Join(layer.Parents, ", "), 2, width, nil, nil)
		}
	}
}

func (output profileTextWriter) defaults(defaults []portage.ProfileDefault, explain bool) {
	output.section("Defaults", fmt.Sprintf("%d variables", len(defaults)))
	width := len("VARIABLE")
	for _, value := range defaults {
		width = max(width, utf8.RuneCountInString(value.Name))
	}
	output.row("VARIABLE", "VALUE", 2, width, color.Bold, color.Bold)
	if len(defaults) == 0 {
		output.row("", "(none)", 2, 0, nil, nil)
	}
	for _, value := range defaults {
		var style func(string) string
		if value.Name == "USE" {
			style = colorProfileFlags
		}
		output.row(value.Name, strconv.Quote(value.Value), 2, width, color.Cyan, style)
		if explain {
			output.contributions(value.Contributions, style)
		}
	}
}

func (output profileTextWriter) globalUse(policies []portage.ProfilePolicy, explain bool) {
	output.section("Global USE policy", "effective entries")
	width := len("use.stable.force")
	output.row("POLICY", "FLAGS", 2, width, color.Bold, color.Bold)
	for _, policy := range policies {
		if !strings.HasPrefix(policy.Name, "use.") {
			continue
		}
		style := color.Green
		if strings.HasSuffix(policy.Name, ".mask") {
			style = color.Red
		}
		value := strings.Join(policy.Values, " ")
		if value == "" {
			value, style = "(none)", nil
		}
		output.row(policy.Name, value, 2, width, color.Cyan, style)
		if explain {
			output.contributions(policy.Contributions, colorProfileFlags)
		}
	}
}

func (output profileTextWriter) policy(policy portage.ProfilePolicy, explain bool) {
	label := "effective entries"
	if policy.Kind == "ordered_rules" {
		label = "ordered rules; evaluated per package"
	}
	name := policy.Name
	if name == "packages" {
		name = "System packages (@system)"
	}
	output.section(name, fmt.Sprintf("%d %s", len(policy.Values), label))
	width := len("PACKAGE")
	for _, value := range policy.Values {
		fields := strings.Fields(value)
		if len(fields) != 0 {
			width = max(width, utf8.RuneCountInString(fields[0]))
		}
	}
	if policy.Kind == "ordered_rules" && len(policy.Values) != 0 {
		output.row("PACKAGE", "FLAGS", 2, width, color.Bold, color.Bold)
	}
	for _, value := range policy.Values {
		if policy.Kind == "ordered_rules" {
			fields := strings.Fields(value)
			output.row(fields[0], strings.Join(fields[1:], " "), 2, width, color.Bold, colorProfileFlags)
		} else {
			var style func(string) string
			if policy.Name == "package.mask" {
				style = color.Red
			} else if policy.Name == "package.unmask" {
				style = color.Green
			}
			output.row("", value, 2, 0, nil, style)
		}
	}
	if len(policy.Values) == 0 {
		output.row("", "(none)", 2, 0, nil, nil)
	}
	if explain {
		output.contributions(policy.Contributions, nil)
	}
}

func (output profileTextWriter) contributions(changes []portage.ProfileContribution, style func(string) string) {
	if len(changes) == 0 {
		return
	}
	output.row("SOURCE", "CONTRIBUTION", 4, output.sourceWidth, color.Bold, color.Bold)
	for _, change := range changes {
		output.row(output.source(change.Source), strconv.Quote(change.Value), 4, output.sourceWidth, color.Blue, style)
	}
}

func (output profileTextWriter) source(source portage.ProfileSource) string {
	return fmt.Sprintf("%s:%d", output.path(source.Path), source.Line)
}

// Measure plain cells before applying ANSI styling so colors never shift columns.
func (output profileTextWriter) row(label, value string, indent, labelWidth int, labelStyle, valueStyle func(string) string) {
	labelWidth = min(labelWidth, max(8, output.width/2-indent))
	gap := 0
	if labelWidth != 0 {
		gap = 2
	}
	padding := strings.Repeat(" ", indent)
	labelLength := utf8.RuneCountInString(label)
	if labelLength > labelWidth {
		fmt.Fprintln(output.writer, padding+profileStyled(label, labelStyle))
		label = ""
		labelLength = 0
	}
	lines := wrapProfileText(value, max(1, output.width-indent-labelWidth-gap))
	for index, line := range lines {
		cell := ""
		if index == 0 {
			cell = profileStyled(label, labelStyle) + strings.Repeat(" ", labelWidth-labelLength)
		} else {
			cell = strings.Repeat(" ", labelWidth)
		}
		fmt.Fprintln(output.writer, padding+cell+strings.Repeat(" ", gap)+profileStyled(line, valueStyle))
	}
}

func profileStyled(text string, style func(string) string) string {
	if style != nil {
		return style(text)
	}
	return text
}

func colorProfileFlags(text string) string {
	parts := strings.Split(text, " ")
	for index, part := range parts {
		name := strings.Trim(part, "\"")
		if strings.HasPrefix(name, "-") {
			parts[index] = color.Red(part)
		} else {
			parts[index] = color.Green(part)
		}
	}
	return strings.Join(parts, " ")
}

// Wrap at spaces without truncating or splitting indivisible tokens. Joining
// the result with spaces recovers the original text, including repeated spaces.
func wrapProfileText(text string, width int) []string {
	var lines []string
	runes := []rune(text)
	for len(runes) > width {
		cut := -1
		for index := 0; index <= width; index++ {
			if runes[index] == ' ' {
				cut = index
			}
		}
		if cut <= 0 {
			cut = width
			for cut < len(runes) && runes[cut] != ' ' {
				cut++
			}
		}
		lines = append(lines, string(runes[:cut]))
		if cut == len(runes) {
			return lines
		}
		runes = runes[cut+1:]
	}
	return append(lines, string(runes))
}
