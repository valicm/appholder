package gui

import (
	"fmt"
	"strings"

	"appholder/internal/manager"
)

func known(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Unknown"
	}
	return value
}
func applicationDetails(app manager.App, library []manager.App) string {
	metadata := app.Metadata
	arch := known(metadata.Architecture)
	if metadata.ArchitectureSource == "runtime-elf" {
		arch += " (AppImage runtime)"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n\n%s\n\nVersion: %s\nArchitecture: %s\nDeveloper: %s\nLicense: %s\nHomepage: %s\nCategories: %s\n\nApplication ID: %s\nDeclared identity: %s\nIdentity source: %s\n\nFile: %s\n", app.Name, known(metadata.Summary), known(app.Version), arch, known(metadata.Developer), known(metadata.License), known(metadata.Homepage), known(strings.Join(metadata.Categories, ", ")), known(metadata.AppID), known(metadata.Identity), known(metadata.IdentitySource), app.Path)
	if metadata.Description != "" {
		text.WriteString("\nDescription\n" + metadata.Description + "\n")
	}
	if len(metadata.Keywords) > 0 {
		text.WriteString("\nKeywords: " + strings.Join(metadata.Keywords, ", ") + "\n")
	}
	if len(metadata.MimeTypes) > 0 {
		text.WriteString("\nSupported file types: " + strings.Join(metadata.MimeTypes, ", ") + "\n")
	}
	text.WriteString("\nOther installed copies\n")
	count := 0
	for _, other := range library {
		if app.Related(other) {
			count++
			fmt.Fprintf(&text, "%s · %s\n%s\n", known(other.Version), known(other.Metadata.Architecture), other.Path)
		}
	}
	if count == 0 {
		text.WriteString("No other copies identified from declared metadata.\n")
	}
	return text.String()
}
