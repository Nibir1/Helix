package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// ReadLine reads a line from stdin with prompt
func ReadLine(prompt string) (string, error) {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	return line, nil
}

// AskYesNo asks the user a yes/no question and returns true for yes.
func AskYesNo(prompt string) (bool, error) {
	for {
		ans, err := ReadLine(prompt + " (yes/no): ")
		if err != nil {
			return false, err
		}
		ans = strings.ToLower(ans)
		if ans == "yes" || ans == "y" {
			return true, nil
		}
		if ans == "no" || ans == "n" {
			return false, nil
		}
		fmt.Println("Please answer 'yes' or 'no'.")
	}
}

// IsOnline performs a lightweight GET to detect internet connectivity.
func IsOnline(timeout time.Duration) bool {
	client := http.Client{Timeout: timeout}
	resp, err := client.Get("https://clients3.google.com/generate_204")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 204
}

// SafeTrim removes dangerous characters/newlines from AI output before executing.
func SafeTrim(s string) string {
	// Basic sanitation: trim, remove trailing semicolons/newlines
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ";\n")
	return s
}
