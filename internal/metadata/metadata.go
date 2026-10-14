// Package metadata inspects AppImages without executing their embedded runtime.
package metadata

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const Revision = 1

type Info struct {
	Name               string   `json:"name,omitempty"`
	Version            string   `json:"version,omitempty"`
	UpdateInfo         string   `json:"update_info,omitempty"`
	Identity           string   `json:"identity,omitempty"`
	IdentitySource     string   `json:"identity_source,omitempty"`
	AppID              string   `json:"app_id,omitempty"`
	AppImageName       string   `json:"appimage_name,omitempty"`
	Architecture       string   `json:"architecture,omitempty"`
	ArchitectureSource string   `json:"architecture_source,omitempty"`
	Summary            string   `json:"summary,omitempty"`
	Description        string   `json:"description,omitempty"`
	Categories         []string `json:"categories,omitempty"`
	Keywords           []string `json:"keywords,omitempty"`
	MimeTypes          []string `json:"mime_types,omitempty"`
	Developer          string   `json:"developer,omitempty"`
	License            string   `json:"license,omitempty"`
	Homepage           string   `json:"homepage,omitempty"`
	PNG                []byte   `json:"-"`
}

// Read supports type-2 images with a SquashFS header in the first 16 MiB.
// unsquashfs is optional; failures leave integration's filename fallback intact.
func Read(path string) (Info, error) {
	var info Info
	if f, err := elf.Open(path); err == nil {
		info.Architecture = map[elf.Machine]string{elf.EM_X86_64: "x86_64", elf.EM_386: "i386", elf.EM_AARCH64: "aarch64", elf.EM_ARM: "arm", elf.EM_RISCV: "riscv", elf.EM_PPC64: "ppc64"}[f.Machine]
		if info.Architecture != "" {
			info.ArchitectureSource = "runtime-elf"
		}
		if section := f.Section(".upd_info"); section != nil && section.Size <= 16384 {
			if data, err := section.Data(); err == nil {
				info.UpdateInfo = strings.TrimSpace(strings.TrimRight(string(data), "\x00"))
			}
		}
		f.Close()
	}
	offset, err := squashOffset(path)
	if err != nil {
		return info, err
	}
	desktop, err := cat(path, offset, "*.desktop", 128*1024)
	if err != nil {
		return info, err
	}
	parsed, parseErr := desktopInfo(string(desktop))
	err = parseErr
	parsed.UpdateInfo = info.UpdateInfo
	if parsed.Architecture == "" {
		parsed.Architecture = info.Architecture
		parsed.ArchitectureSource = info.ArchitectureSource
	}
	info = parsed
	if err != nil {
		return Info{UpdateInfo: info.UpdateInfo, Architecture: info.Architecture, ArchitectureSource: info.ArchitectureSource}, err
	}
	component, _ := readAppStream(path, offset, rootDesktopName(path, offset))
	applyAppStream(&info, component)
	// Only accept PNG bytes; never render untrusted SVG or HTML in the UI.
	if icon, err := cat(path, offset, ".DirIcon", 4*1024*1024); err == nil && bytes.HasPrefix(icon, []byte("\x89PNG\r\n\x1a\n")) {
		info.PNG = icon
	}
	return info, nil
}

func squashOffset(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16*1024*1024))
	if err != nil {
		return 0, err
	}
	for start := 0; start+96 <= len(data); {
		relative := bytes.Index(data[start:], []byte("hsqs"))
		if relative < 0 {
			break
		}
		i := start + relative
		if i+96 <= len(data) && binary.LittleEndian.Uint16(data[i+28:i+30]) == 4 && binary.LittleEndian.Uint16(data[i+30:i+32]) == 0 {
			return int64(i), nil
		}
		start = i + 4
	}
	return 0, fmt.Errorf("no supported SquashFS header found")
}

type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.Len() {
		return 0, fmt.Errorf("metadata exceeds size limit")
	}
	return b.Buffer.Write(p)
}
func cat(path string, offset int64, pattern string, limit int) ([]byte, error) {
	return runSquash(path, offset, []string{"-cat"}, []string{pattern}, limit)
}
func runSquash(path string, offset int64, options, files []string, limit int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{"-processors", "1", "-no-progress", "-offset", strconv.FormatInt(offset, 10)}
	args = append(args, options...)
	args = append(args, path)
	args = append(args, files...)
	cmd := exec.CommandContext(ctx, "unsquashfs", args...)
	output := &cappedBuffer{max: limit}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("read AppImage metadata: %w", err)
	}
	return output.Bytes(), nil
}
func parseDesktop(data string) (string, string, error) {
	info, err := desktopInfo(data)
	return info.Name, info.Version, err
}
func desktopInfo(data string) (Info, error) {
	var info Info
	section := ""
	count := 0
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			if section == "[Desktop Entry]" {
				count++
			}
			continue
		}
		if section != "[Desktop Entry]" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = decode(value)
		switch key {
		case "Name":
			info.Name = value
		case "Comment":
			info.Summary = value
		case "X-AppImage-Name":
			info.AppImageName = value
		case "X-AppImage-Version":
			info.Version = value
		case "X-AppImage-Arch":
			info.Architecture = value
			info.ArchitectureSource = "desktop"
		case "Categories":
			info.Categories = cleanList(strings.Split(value, ";"))
		case "Keywords":
			info.Keywords = cleanList(strings.Split(value, ";"))
		case "MimeType":
			info.MimeTypes = cleanList(strings.Split(value, ";"))
		}
	}
	if count != 1 || info.Name == "" {
		return info, fmt.Errorf("expected one desktop entry with a name")
	}
	if info.AppImageName != "" {
		info.Identity = "appimage:" + info.AppImageName
		info.IdentitySource = "appimage"
	}
	return info, nil
}
func cleanList(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out
}
func decode(value string) string {
	return strings.NewReplacer("\\s", " ", "\\n", " ", "\\r", " ", "\\t", " ", "\\\\", "\\").Replace(value)
}
