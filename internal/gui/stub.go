//go:build !desktop || !linux || !cgo

package gui

import "fmt"

func Run(socket string) error {
	return fmt.Errorf("GUI requires Linux, cgo and a build with -tags desktop")
}
func Available() bool { return false }

func Open(socket, path string) error { return Run(socket) }

func Tray(socket string) error { return Run(socket) }
