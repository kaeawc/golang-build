package main

import (
	"fmt"
	"io"
)

// runHeadless mirrors the wizard flow without a TTY. Used by CI,
// scripts, and the --yes flag. It applies preset defaults verbatim;
// no per-question overrides yet.
func runHeadless(out io.Writer, target, presetName string) (string, error) {
	p := presetByName(presetName)
	if p == nil {
		return "", fmt.Errorf("unknown preset %q (valid: minimal, standard, full)", presetName)
	}
	cfg := fromPreset(*p)
	path, err := writeConfig(target, cfg)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(out, "wrote %s (preset: %s)\n", path, p.Name)
	return path, nil
}
