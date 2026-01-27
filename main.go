package main

import (
	"bufio"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed sensitive_words.txt
var embeddedSensitive string

func main() {
	path := flag.String("path", "", "directory or file path to scan for yaml files")
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

	stats := &procStats{OpsPerFile: make(map[string]int)}
	err = processPath(*path, words, *dry, stats)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// print summary
	fmt.Printf("\nSummary:\n")
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
		// write back
		tmp := path + ".tmp_iceminus"
		err = os.WriteFile(tmp, []byte(strings.Join(outLines, "")), 0644)
		if err != nil {
			return 0, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return 0, err
		}
	}
	return matchedLines, nil
}
