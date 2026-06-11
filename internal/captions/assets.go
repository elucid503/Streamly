package captions

import (
	"os"
	"path/filepath"
)

const fontName = "font.ttf"

var osExecutable = os.Executable

// FontPath returns assets/font.ttf from the working directory or next to the binary.
func FontPath() (string, error) {

	candidates := candidateFontPaths()

	for _, path := range candidates {

		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			abs, err := filepath.Abs(path)
			if err != nil {
				return "", err
			}
			return abs, nil
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

	abs, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return "", err
	}

	return abs, nil

}

func candidateFontPaths() []string {

	candidates := []string{
		filepath.Join("assets", fontName),
	}

	if dir := os.Getenv("STREAMLY_ASSETS_DIR"); dir != "" {
		candidates = append(candidates, filepath.Join(dir, fontName))
	}

	if executable, err := osExecutable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}

		exeDir := filepath.Dir(executable)
		candidates = append(candidates, filepath.Join(exeDir, "assets", fontName))
		candidates = append(candidates, filepath.Join(filepath.Dir(exeDir), "assets", fontName))
	}

	return candidates

}