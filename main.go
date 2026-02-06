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
	sensPath := flag.String("sensitive", "sensitive_words.txt", "path to sensitive words file")
	flag.Parse()

	if *path == "" {
		fmt.Fprintln(os.Stderr, "usage: iceminus --path <path> [--dry-run] [--sensitive <file>]")
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
	absPath, _ := filepath.Abs(*path)
	folderPath := absPath
	if info, statErr := os.Stat(*path); statErr == nil {
		if !info.IsDir() {
			folderPath = filepath.Dir(absPath)
		}
	}

	stats := &procStats{OpsPerFile: make(map[string]int)}
	err = processPath(*path, words, *dry, stats)
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

func loadSensitive(path string) ([]string, error) {
	var src string
	// use embedded content by default when the provided path equals the default
	if path == "" || path == "sensitive_words.txt" {
		src = embeddedSensitive
	} else {
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

func processPath(root string, words []string, dryRun bool, stats *procStats) error {
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
				cnt, err := processFile(path, words, dryRun)
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
	cnt, err := processFile(root, words, dryRun)
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

func processFile(path string, words []string, dryRun bool) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	var outLines []string
	r := bufio.NewReader(f)
	lineNo := 0
	modified := false
	matchedLines := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return 0, err
		}
		// handle last line without newline
		rawLine := line
		if err == io.EOF && len(line) == 0 {
			break
		}
		// remove trailing newline for processing but keep it for reconstruction
		hasNL := strings.HasSuffix(rawLine, "\n")
		content := rawLine
		if hasNL {
			content = rawLine[:len(rawLine)-1]
		}
		lineNo++

		// skip already commented lines only when the very first character is '#'
		if strings.HasPrefix(content, "#") {
			outLines = append(outLines, rawLine)
			if err == io.EOF {
				break
			}
			continue
		}

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
			fmt.Printf("%s:%d -> %s\n", path, lineNo, strings.Join(matched, ", "))
			if dryRun {
				outLines = append(outLines, rawLine)
			} else {
				// add comment marker at line start
				newLine := "# " + content
				if hasNL {
					newLine += "\n"
				}
				outLines = append(outLines, newLine)
			}
		} else {
			outLines = append(outLines, rawLine)
		}

		if err == io.EOF {
			break
		}
	}

	if modified && !dryRun {
		// write back to a temp file then replace the original.
		tmp := path + ".tmp_iceminus"
		data := []byte(strings.Join(outLines, ""))
		if err = os.WriteFile(tmp, data, 0644); err != nil {
			return 0, err
		}
		// Try atomic rename first.
		if err = os.Rename(tmp, path); err == nil {
			return matchedLines, nil
		}
		// On Windows, rename may fail if the destination is locked or read-only.
		// Try to remove destination then rename.
		if remErr := os.Remove(path); remErr == nil {
			if err = os.Rename(tmp, path); err == nil {
				return matchedLines, nil
			}
		} else {
			// attempt to make file writable and remove again
			_ = os.Chmod(path, 0644)
			if remErr2 := os.Remove(path); remErr2 == nil {
				if err = os.Rename(tmp, path); err == nil {
					return matchedLines, nil
				}
			}
		}
		// Fallback: overwrite the original file contents.
		tmpData, readErr := os.ReadFile(tmp)
		if readErr != nil {
			return 0, fmt.Errorf("rename failed: %v; remove failed: %v; read tmp failed: %v", err, os.Remove(path), readErr)
		}
		of, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if openErr != nil {
			// try to change permissions then open again
			_ = os.Chmod(path, 0644)
			of, openErr = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if openErr != nil {
				return 0, fmt.Errorf("failed to overwrite file after rename failure: %v; open error: %v", err, openErr)
			}
		}
		if _, writeErr := of.Write(tmpData); writeErr != nil {
			_ = of.Close()
			return 0, writeErr
		}
		_ = of.Close()
		_ = os.Remove(tmp)
	}
	return matchedLines, nil
}
