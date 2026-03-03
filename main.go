package main

import (
	"bufio"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed sensitive_words.txt
var embeddedSensitive string

const embeddedSentinel = "@embedded"

func main() {
	// determine default path for --path on Windows
	defaultPath := ""
	if runtime.GOOS == "windows" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			defaultPath = filepath.Join(home, "AppData", "Roaming", "Rime", "cn_dicts")
		}
	}

	path := flag.String("path", defaultPath, "directory or file path to scan for yaml files")
	dry := flag.Bool("dry-run", false, "print what would be changed without modifying files")
	sensPath := flag.String("sensitive", embeddedSentinel, "path to sensitive words file, or @embedded to use embedded list")
	flag.Parse()

	if *path == "" {
		fmt.Fprintln(os.Stderr, "usage: iceminus --path <path> [--dry-run] [--sensitive <file|@embedded>]")
		os.Exit(2)
	}

	words, err := loadSensitive(*sensPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load sensitive words: %v\n", err)
		os.Exit(1)
	}
	if len(words) == 0 {
		fmt.Fprintln(os.Stderr, "no sensitive words found; nothing to do")
		os.Exit(0)
	}

	// determine folder path (absolute). If a file was provided, use its parent dir.
	absPath, absErr := filepath.Abs(*path)
	if absErr != nil {
		absPath = *path // fallback
	}
	folderPath := absPath
	if info, statErr := os.Stat(*path); statErr == nil {
		if !info.IsDir() {
			folderPath = filepath.Dir(absPath)
		}
	}

	stats := newProcStats()
	err = processPath(*path, words, *dry, stats, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// print summary
	fmt.Printf("\nSummary:\n")
	fmt.Printf("  scanned folder: %s\n", folderPath)
	fmt.Printf("  yaml files scanned: %d\n", stats.FilesScanned)
	fmt.Printf("  files with matches: %d\n", stats.FilesWithMatches)
	fmt.Printf("  total matched lines: %d\n", stats.TotalMatches)
	if len(stats.OpsPerFile) > 0 {
		fmt.Printf("  per-file operations:\n")
		for f, c := range stats.OpsPerFile {
			fmt.Printf("    %s: %d\n", f, c)
		}
	}
}

type procStats struct {
	FilesScanned     int
	FilesWithMatches int
	TotalMatches     int
	OpsPerFile       map[string]int
}

func newProcStats() *procStats {
	return &procStats{OpsPerFile: make(map[string]int)}
}

func loadSensitive(path string) ([]string, error) {
	var src string

	// Use embedded content only when explicitly requested (or empty).
	switch strings.TrimSpace(path) {
	case "", embeddedSentinel:
		src = embeddedSensitive
	default:
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		src = string(b)
	}

	var words []string
	scanner := bufio.NewScanner(strings.NewReader(src))
	for scanner.Scan() {
		t := strings.TrimSpace(scanner.Text())
		if t == "" {
			continue
		}
		words = append(words, t)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return words, nil
}

func processPath(root string, words []string, dryRun bool, stats *procStats, out io.Writer) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".yaml" || ext == ".yml" {
				stats.FilesScanned++
				cnt, err := processFile(path, words, dryRun, out)
				if err != nil {
					return err
				}
				if cnt > 0 {
					stats.FilesWithMatches++
					stats.TotalMatches += cnt
					stats.OpsPerFile[path] = cnt
				}
			}
			return nil
		})
	}

	// single file
	stats.FilesScanned++
	cnt, err := processFile(root, words, dryRun, out)
	if err != nil {
		return err
	}
	if cnt > 0 {
		stats.FilesWithMatches++
		stats.TotalMatches += cnt
		stats.OpsPerFile[root] = cnt
	}
	return nil
}

func processFile(path string, words []string, dryRun bool, out io.Writer) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	// read-only file; close error is not actionable in practice
	defer func() { _ = f.Close() }()

	var outLines []string
	r := bufio.NewReader(f)

	lineNo := 0
	modified := false
	matchedLines := 0

	for {
		line, readErr := r.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return 0, readErr
		}
		if readErr == io.EOF && len(line) == 0 {
			break
		}

		eof := (readErr == io.EOF)

		rawLine := line
		hasNL := strings.HasSuffix(rawLine, "\n")
		content := rawLine
		if hasNL {
			content = rawLine[:len(rawLine)-1]
		}
		lineNo++

		// Determine if we keep line as-is
		keepRaw := false
		var newLine string

		// Skip already-commented lines only when the very first character is '#'
		if strings.HasPrefix(content, "#") {
			keepRaw = true
		} else {
			var matched []string
			for _, w := range words {
				if w == "" {
					continue
				}
				if strings.Contains(content, w) {
					matched = append(matched, w)
				}
			}

			if len(matched) > 0 {
				modified = true
				matchedLines++

				if _, werr := fmt.Fprintf(out, "%s:%d -> %s\n", path, lineNo, strings.Join(matched, ", ")); werr != nil {
					return 0, werr
				}

				if dryRun {
					keepRaw = true
				} else {
					newLine = "# " + content
					if hasNL {
						newLine += "\n"
					}
				}
			} else {
				keepRaw = true
			}
		}

		if keepRaw {
			outLines = append(outLines, rawLine)
		} else {
			outLines = append(outLines, newLine)
		}

		// Single EOF break point (avoid scattered breaks/continues)
		if eof {
			break
		}
	}

	if modified && !dryRun {
		data := []byte(strings.Join(outLines, ""))
		if err := writeFileAtomic(path, data); err != nil {
			return 0, err
		}
	}

	return matchedLines, nil
}

func writeFileAtomic(path string, data []byte) (err error) {
	tmp := path + ".tmp_iceminus"

	if err = os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	// Ensure tmp is always cleaned up, whether rename succeeded or we fell back to copy.
	// (On successful rename, tmp no longer exists under this name; Remove then is harmless.)
	defer func() { _ = os.Remove(tmp) }()

	// 1) Try rename first.
	if err = os.Rename(tmp, path); err == nil {
		return nil
	}

	// 2) If destination exists/locked/read-only (common on Windows), try remove+rename.
	if remErr := os.Remove(path); remErr != nil {
		_ = os.Chmod(path, 0644)
		_ = os.Remove(path)
	}
	if err2 := os.Rename(tmp, path); err2 == nil {
		return nil
	}

	// 3) Fallback: overwrite in-place.
	//
	// Risk: O_TRUNC truncates immediately; if Write/Close fails, data loss is possible.
	// Mitigation: best-effort backup original content in memory and attempt restore on failure.
	orig, _ := os.ReadFile(path) // best effort; ignore errors

	of, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if openErr != nil {
		_ = os.Chmod(path, 0644)
		of, openErr = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if openErr != nil {
			return fmt.Errorf("failed to overwrite file after rename failure: %w", openErr)
		}
	}

	_, werr := of.Write(data)
	cerr := of.Close()

	if werr == nil && cerr == nil {
		return nil
	}

	// Attempt restore if we had a backup; best-effort only.
	if orig != nil {
		_ = os.WriteFile(path, orig, 0644)
	}

	if werr != nil && cerr != nil {
		return fmt.Errorf("write failed: %v; close failed: %v", werr, cerr)
	}
	if werr != nil {
		return werr
	}
	return cerr
}
