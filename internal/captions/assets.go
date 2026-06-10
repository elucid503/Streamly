package captions

import (
	"os"
	"path/filepath"
)

const fontName = "font.ttf"

// FontPath returns assets/font.ttf from the working directory or next to the binary.
func FontPath() (string, error) {

	candidates := []string{
		filepath.Join("assets", fontName),
	}

	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "assets", fontName))
	}

	for _, path := range candidates {

		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}

	}

	return "", ErrNoFont

}

// FontsDir returns the directory containing font.ttf for libass fontsdir=.
func FontsDir() (string, error) {

	path, err := FontPath()

	if err != nil {
		return "", err
	}

	return filepath.Dir(path), nil

}
