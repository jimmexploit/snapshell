package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// genericFamilies are fontconfig aliases that are always valid in a Pango
// font description even though fc-list does not report them as concrete
// fonts. The default popup font "Sans 13" is one of them.
var genericFamilies = []string{"Sans", "Serif", "Monospace"}

// listFontFamilies runs fc-list and returns every font family the system
// has, deduplicated and sorted, with the generic Pango families always
// present. A missing fc-list is a named, actionable error (repo subprocess
// rule — never a raw exec error).
func listFontFamilies() ([]string, error) {
	bin, err := exec.LookPath("fc-list")
	if err != nil {
		return nil, fmt.Errorf("fc-list not found on PATH — install fontconfig (e.g. apt install fontconfig) to list fonts")
	}
	out, err := exec.Command(bin, "--format=%{family}\n").Output()
	if err != nil {
		return nil, fmt.Errorf("list fonts: %v", err)
	}
	return parseFamilies(string(out)), nil
}

// parseFamilies turns fc-list output into a sorted, deduplicated family
// list. A single font can declare several family names ("Noto Sans, Noto
// Sans CJK SC"), each of which is a valid Pango family of its own, so every
// comma-separated entry is kept.
func parseFamilies(output string) []string {
	seen := map[string]bool{}
	var families []string
	for _, line := range strings.Split(output, "\n") {
		for _, fam := range strings.Split(line, ",") {
			fam = strings.TrimSpace(fam)
			if fam == "" || seen[fam] {
				continue
			}
			seen[fam] = true
			families = append(families, fam)
		}
	}
	for _, g := range genericFamilies {
		if !seen[g] {
			seen[g] = true
			families = append(families, g)
		}
	}
	sort.Strings(families)
	return families
}

// newListFontsCmd prints every font family on the system so the user can
// pick a value for [popup].font.
func newListFontsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-fonts",
		Short: "List every font family on the system (for popup.font)",
		Long: "List the font family names you can put in [popup].font — " +
			"e.g. \"JetBrains Mono 13\". Output is deduplicated and sorted; " +
			"the generic Pango families (Sans, Serif, Monospace) are always " +
			"included even when no concrete font provides them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			families, err := listFontFamilies()
			if err != nil {
				return err
			}
			for _, f := range families {
				fmt.Println(f)
			}
			return nil
		},
	}
}
