package main

import (
	"bufio"
	"fmt"
	"os"
)

// AppendHistory appends a line to the history file (creates it if missing).
func AppendHistory(path, line string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = fmt.Fprintln(f, line)
	return err
}

// LoadHistory returns a slice of previous lines; returns empty slice on any error.
func LoadHistory(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		// Not fatal — return empty history
		return []string{}, nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}
