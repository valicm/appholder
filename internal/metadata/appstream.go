package metadata

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
)

type localized struct {
	Lang string `xml:"lang,attr"`
	Text string `xml:",chardata"`
}
type component struct {
	Type         string      `xml:"type,attr"`
	ID           string      `xml:"id"`
	Names        []localized `xml:"name"`
	Summaries    []localized `xml:"summary"`
	Descriptions []struct {
		Lang string `xml:"lang,attr"`
		XML  string `xml:",innerxml"`
	} `xml:"description"`
	Developers []localized `xml:"developer_name"`
	Developer  struct {
		Names []localized `xml:"name"`
	} `xml:"developer"`
	License string `xml:"project_license"`
	URLs    []struct {
		Type string `xml:"type,attr"`
		Text string `xml:",chardata"`
	} `xml:"url"`
	Categories  []string `xml:"categories>category"`
	Launchables []struct {
		Type string `xml:"type,attr"`
		Text string `xml:",chardata"`
	} `xml:"launchable"`
}

func defaultText(values []localized) string {
	for _, v := range values {
		if v.Lang == "" {
			return strings.TrimSpace(v.Text)
		}
	}
	return ""
}
func plainDescription(raw string) string {
	decoder := xml.NewDecoder(strings.NewReader("<root>" + raw + "</root>"))
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ""
		}
		switch value := token.(type) {
		case xml.CharData:
			text.WriteString(string(value))
		case xml.StartElement:
			if value.Name.Local == "li" {
				text.WriteString("\n• ")
			}
		case xml.EndElement:
			if value.Name.Local == "p" || value.Name.Local == "li" {
				text.WriteString("\n")
			}
		}
	}
	var lines []string
	for _, line := range strings.Split(text.String(), "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
func parseAppStream(data []byte) (component, error) {
	var root struct{ XMLName xml.Name }
	var c component
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&c); err != nil {
		return c, err
	}
	if err := xml.Unmarshal(data, &root); err != nil {
		return c, err
	}
	if root.XMLName.Local != "component" {
		return c, fmt.Errorf("expected a single AppStream component")
	}
	if c.Type != "desktop-application" || strings.TrimSpace(c.ID) == "" {
		return c, fmt.Errorf("not a desktop application component")
	}
	return c, nil
}
func (c component) matchesDesktop(name string) bool {
	for _, launchable := range c.Launchables {
		if launchable.Type == "desktop-id" && strings.TrimSpace(launchable.Text) == name {
			return true
		}
	}
	id := strings.TrimSpace(c.ID)
	return name != "" && (id == name || strings.TrimSuffix(id, ".desktop") == strings.TrimSuffix(name, ".desktop"))
}
func readAppStream(path string, offset int64, desktopName string) (*component, error) {
	var components []component
	seen := map[string]bool{}
	for _, dir := range []string{"usr/share/metainfo", "usr/share/appdata"} {
		listing, err := runSquash(path, offset, []string{"-lc"}, []string{dir}, 512*1024)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(listing), "\n") {
			file := strings.TrimPrefix(strings.TrimSpace(line), "squashfs-root/")
			if !strings.HasPrefix(file, dir+"/") || (!strings.HasSuffix(file, ".metainfo.xml") && !strings.HasSuffix(file, ".appdata.xml")) || seen[file] {
				continue
			}
			seen[file] = true
			if len(seen) > 16 {
				return nil, fmt.Errorf("too many AppStream metadata files")
			}
			data, err := runSquash(path, offset, []string{"-no-wildcards", "-cat"}, []string{file}, 512*1024)
			if err != nil {
				continue
			}
			c, err := parseAppStream(data)
			if err != nil {
				continue
			}
			components = append(components, c)
		}
	}
	var matching []component
	for _, c := range components {
		if c.matchesDesktop(desktopName) {
			matching = append(matching, c)
		}
	}
	if len(matching) == 1 {
		return &matching[0], nil
	}
	if len(matching) > 1 {
		return nil, fmt.Errorf("multiple matching AppStream components")
	}
	if len(components) == 1 && (desktopName == "" || len(components[0].Launchables) == 0) {
		return &components[0], nil
	}
	return nil, nil
}
func applyAppStream(info *Info, c *component) {
	if c == nil {
		return
	}
	info.AppID = strings.TrimSpace(c.ID)
	info.Identity = "appstream:" + info.AppID
	info.IdentitySource = "appstream"
	if name := defaultText(c.Names); name != "" && info.Name == "" {
		info.Name = name
	}
	if summary := defaultText(c.Summaries); summary != "" {
		info.Summary = summary
	}
	for _, description := range c.Descriptions {
		if description.Lang == "" {
			info.Description = plainDescription(description.XML)
			break
		}
	}
	info.Developer = defaultText(c.Developer.Names)
	if info.Developer == "" {
		info.Developer = defaultText(c.Developers)
	}
	info.License = strings.TrimSpace(c.License)
	if len(info.Categories) == 0 {
		info.Categories = cleanList(c.Categories)
	}
	for _, link := range c.URLs {
		if link.Type == "homepage" {
			raw := strings.TrimSpace(link.Text)
			u, err := url.Parse(raw)
			if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil {
				info.Homepage = raw
			}
			break
		}
	}
}
func rootDesktopName(path string, offset int64) string {
	data, err := runSquash(path, offset, []string{"-lc", "-max-depth", "1"}, []string{"*.desktop"}, 128*1024)
	if err != nil {
		return ""
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		file := strings.TrimPrefix(strings.TrimSpace(line), "squashfs-root/")
		file, _, _ = strings.Cut(file, " -> ")
		if !strings.Contains(file, "/") && strings.HasSuffix(file, ".desktop") {
			names = append(names, filepath.Base(file))
		}
	}
	if len(names) == 1 {
		return names[0]
	}
	return ""
}
