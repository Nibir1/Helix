// internal/rag/manindexer.go

package rag

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"helix/internal/shell"

	"github.com/fatih/color"
)

// MANPage represents a processed manual page
type MANPage struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Synopsis    string   `json:"synopsis"`
	Options     []string `json:"options"`
	Examples    []string `json:"examples"`
	FullText    string   `json:"full_text"`
	Category    string   `json:"category"`
	Path        string   `json:"path"`
}

// MANIndexer handles scanning and processing MAN pages.
// NOTE: This version is PROGRESS-BAR FRIENDLY: it avoids
// noisy logs during indexing. The outer RAG system drives
// progress with GetIndexedCount() + renderProgressBarD().
type MANIndexer struct {
	env        shell.Env
	indexDir   string
	indexed    map[string]MANPage
	mu         sync.RWMutex
	categories []string

	discoveredTotal int
}

// NewMANIndexer creates a new MAN page indexer
func NewMANIndexer(env shell.Env) *MANIndexer {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp"
	}

	indexDir := filepath.Join(homeDir, ".helix", "man_index")

	return &MANIndexer{
		env:        env,
		indexDir:   indexDir,
		indexed:    make(map[string]MANPage),
		categories: []string{"1", "2", "3", "4", "5", "6", "7", "8"},
	}
}

// IndexAvailableManPages scans and indexes all available MAN pages.
// Progress is shown by RAGSystem via GetIndexedCount(); we keep
// logging here minimal and non-spammy.
func (mi *MANIndexer) IndexAvailableManPages() error {
	color.Blue("Scanning for MAN pages...")

	if err := mi.ensureIndexDir(); err != nil {
		return fmt.Errorf("failed to create index directory: %w", err)
	}

	manPath := mi.getMANPath()
	color.Cyan("MAN path: %s", manPath)

	var wg sync.WaitGroup
	pageChan := make(chan string, 100)
	resultChan := make(chan MANPage, 100)

	// Start workers to process MAN pages
	workerCount := 6
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go mi.manPageWorker(&wg, pageChan, resultChan)
	}

	// Collect results
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Discover MAN page commands (quiet – just summary)
	go mi.findMANPages(manPath, pageChan)

	processed := 0
	for page := range resultChan {
		mi.mu.Lock()
		mi.indexed[page.Name] = page
		mi.mu.Unlock()
		processed++
	}

	if processed == 0 {
		color.Yellow("MAN page indexing finished but no pages were usable")
	} else {
		color.Green("MAN page indexing completed! Indexed %d pages", processed)
	}

	return mi.saveIndex()
}

// findMANPages discovers candidate commands for MAN page processing.
// It only prints a small summary so it doesn't fight the progress bar.
func (mi *MANIndexer) findMANPages(manPath string, pageChan chan<- string) {
	defer close(pageChan)

	color.Cyan("Using smart MAN page discovery...")

	totalFound := 0

	methods := []func(chan<- string) int{
		mi.tryManKEnhanced,
		mi.tryDirectoryScan,
	}

	for _, method := range methods {
		count := method(pageChan)
		if count > 0 {
			totalFound += count
		}
	}

	mi.discoveredTotal = totalFound

	if totalFound == 0 {
		color.Red("No MAN pages found using any discovery method")
		color.Yellow("MAN pages might not be installed or MANPATH is misconfigured")
	} else {
		color.Green("Discovered %d candidate commands for MAN indexing (filtered to useful ones)", totalFound)
	}
}

// scanMANCategoryEnhanced scans a single manN directory.
// Quiet by default to avoid log spam.
func (mi *MANIndexer) scanMANCategoryEnhanced(categoryPath string, ch chan<- string, seen map[string]bool) int {
	entries, err := os.ReadDir(categoryPath)
	if err != nil {
		return 0
	}

	count := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Handle ls.1, ls.1.gz, ls.1.bz2, etc.
		if strings.Contains(name, ".") {
			command := strings.Split(name, ".")[0]
			if !seen[command] && len(command) > 1 {
				seen[command] = true
				count++

				if mi.isUsefulCommand(command) {
					ch <- command
				}
			}
		}
	}

	return count
}

// tryManKEnhanced discovers commands using `man -k .` (quiet)
func (mi *MANIndexer) tryManKEnhanced(ch chan<- string) int {
	cmd := exec.Command("man", "-k", ".")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	count := 0
	lines := strings.Split(string(output), "\n")
	seen := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// e.g. "ls(1) - list directory contents"
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}

		command := parts[0]
		// Strip "(1)" etc
		command = strings.Split(command, "(")[0]
		// Strip git- prefix (we still treat "git" as main command)
		command = strings.TrimPrefix(command, "git-")

		if !seen[command] && len(command) > 1 {
			seen[command] = true
			if mi.isUsefulCommand(command) {
				ch <- command
			}
			count++
		}
	}

	return count
}

// tryDirectoryScan does a direct scan of MAN directories (quiet)
func (mi *MANIndexer) tryDirectoryScan(ch chan<- string) int {
	manPath := mi.getMANPath()
	paths := strings.Split(manPath, ":")
	seen := make(map[string]bool)
	count := 0

	for _, path := range paths {
		for _, category := range mi.categories {
			categoryPath := filepath.Join(path, "man"+category)
			count += mi.scanMANCategoryEnhanced(categoryPath, ch, seen)
		}
	}

	return count
}

// Worker: processes individual MAN pages
func (mi *MANIndexer) manPageWorker(wg *sync.WaitGroup, pageChan <-chan string, resultChan chan<- MANPage) {
	defer wg.Done()

	for command := range pageChan {
		// final guard, though we mostly filter earlier
		if !mi.isUsefulCommand(command) {
			continue
		}

		page, err := mi.processMANPage(command)
		if err != nil {
			continue
		}
		resultChan <- page
	}
}

// processMANPage extracts a single MAN page
func (mi *MANIndexer) processMANPage(command string) (MANPage, error) {
	cmd := exec.Command("man", command)
	output, err := cmd.Output()
	if err != nil {
		return MANPage{}, fmt.Errorf("failed to get MAN page for %s: %w", command, err)
	}

	content := string(output)
	return mi.parseMANContent(command, content), nil
}

// parseMANContent parses a MAN page into structured fields
func (mi *MANIndexer) parseMANContent(command, content string) MANPage {
	page := MANPage{
		Name:     command,
		FullText: content,
	}

	lines := strings.Split(content, "\n")
	var currentSection string
	var sectionContent strings.Builder

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// crude section header heuristic
		if strings.ToUpper(line) == line && len(line) > 0 && !strings.Contains(line, " ") {
			mi.processSection(currentSection, sectionContent.String(), &page)

			currentSection = line
			sectionContent.Reset()
			continue
		}

		sectionContent.WriteString(line + "\n")
	}

	// last section
	mi.processSection(currentSection, sectionContent.String(), &page)

	if page.Description == "" {
		page.Description = mi.extractDescription(content)
	}

	return page
}

// processSection maps a MAN section to our struct
func (mi *MANIndexer) processSection(section, content string, page *MANPage) {
	switch strings.ToUpper(section) {
	case "NAME":
		page.Description = mi.extractNameDescription(content)
	case "SYNOPSIS":
		page.Synopsis = mi.cleanSynopsis(content)
	case "DESCRIPTION":
		if page.Description == "" {
			page.Description = mi.extractFirstParagraph(content)
		}
	case "OPTIONS":
		page.Options = mi.extractOptions(content)
	case "EXAMPLES":
		page.Examples = mi.extractExamples(content)
	}
}

// extractNameDescription parses the NAME section ("cmd - description")
func (mi *MANIndexer) extractNameDescription(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.Contains(line, " - ") {
			parts := strings.SplitN(line, " - ", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return mi.extractFirstParagraph(content)
}

// extractFirstParagraph gets the first non-empty paragraph
func (mi *MANIndexer) extractFirstParagraph(content string) string {
	lines := strings.Split(content, "\n")
	var paragraph strings.Builder

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if paragraph.Len() > 0 {
				break
			}
			continue
		}
		if paragraph.Len() > 0 {
			paragraph.WriteString(" ")
		}
		paragraph.WriteString(line)
	}

	result := paragraph.String()
	if len(result) > 200 {
		result = result[:200] + "..."
	}
	return result
}

// cleanSynopsis simplifies SYNOPSIS into a single short line
func (mi *MANIndexer) cleanSynopsis(content string) string {
	space := regexp.MustCompile(`\s+`)
	content = space.ReplaceAllString(content, " ")

	lines := strings.Split(content, "\n")
	if len(lines) > 0 {
		s := strings.TrimSpace(lines[0])
		if len(s) > 150 {
			s = s[:150] + "..."
		}
		return s
	}
	return content
}

// extractOptions grabs the top N option lines
func (mi *MANIndexer) extractOptions(content string) []string {
	var options []string
	lines := strings.Split(content, "\n")

	pat := regexp.MustCompile(`^\s*[-]{1,2}[a-zA-Z0-9]`)

	for _, line := range lines {
		if pat.MatchString(line) {
			opt := strings.TrimSpace(line)
			if len(opt) > 0 && len(opt) < 100 {
				options = append(options, opt)
			}
		}
	}

	if len(options) > 10 {
		return options[:10]
	}
	return options
}

// extractExamples grabs a few example blocks
func (mi *MANIndexer) extractExamples(content string) []string {
	var examples []string
	lines := strings.Split(content, "\n")

	pat := regexp.MustCompile(`^\s*(?:\$|#|>)`)
	var cur strings.Builder

	for _, line := range lines {
		if pat.MatchString(line) {
			if cur.Len() > 0 {
				examples = append(examples, cur.String())
				cur.Reset()
			}
			cur.WriteString(strings.TrimSpace(line))
		} else if cur.Len() > 0 && strings.TrimSpace(line) != "" {
			cur.WriteString(" " + strings.TrimSpace(line))
		}
	}

	if cur.Len() > 0 {
		examples = append(examples, cur.String())
	}

	if len(examples) > 5 {
		return examples[:5]
	}
	return examples
}

// extractDescription fallback: first decent-looking line
func (mi *MANIndexer) extractDescription(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 20 && len(line) < 200 && !strings.HasPrefix(line, ".") {
			return line
		}
	}
	return "No description available"
}

// getMANPath detects MANPATH (with macOS tweaks)
func (mi *MANIndexer) getMANPath() string {
	if manPath := os.Getenv("MANPATH"); manPath != "" {
		return manPath
	}

	if mi.env.OSName == "darwin" {
		paths := []string{
			"/usr/share/man",
			"/usr/local/share/man",
			"/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/usr/share/man",
			"/Library/Developer/CommandLineTools/Toolchains/XcodeDefault.xctoolchain/usr/share/man",
			"/Library/Developer/CommandLineTools/usr/share/man",
			"/Applications/Xcode.app/Contents/Developer/usr/share/man",
		}

		var valid []string
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				valid = append(valid, p)
			}
		}
		if len(valid) > 0 {
			return strings.Join(valid, ":")
		}
	}

	return "/usr/share/man:/usr/local/share/man"
}

// ensureIndexDir creates the MAN index directory
func (mi *MANIndexer) ensureIndexDir() error {
	return os.MkdirAll(mi.indexDir, 0o755)
}

// saveIndex currently just logs summary; persistence is handled by VectorStore.
func (mi *MANIndexer) saveIndex() error {
	mi.mu.RLock()
	defer mi.mu.RUnlock()

	color.Green("MAN page index ready (%d pages)", len(mi.indexed))
	return nil
}

// GetIndexedCount returns number of indexed pages
func (mi *MANIndexer) GetIndexedCount() int {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	return len(mi.indexed)
}

// GetPage retrieves a MAN page by name
func (mi *MANIndexer) GetPage(name string) (MANPage, bool) {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	page, ok := mi.indexed[name]
	return page, ok
}

// SearchPages does a simple text search over indexed MAN pages
func (mi *MANIndexer) SearchPages(query string) []MANPage {
	mi.mu.RLock()
	defer mi.mu.RUnlock()

	var results []MANPage
	q := strings.ToLower(query)

	for _, page := range mi.indexed {
		if strings.Contains(strings.ToLower(page.Name), q) ||
			strings.Contains(strings.ToLower(page.Description), q) ||
			strings.Contains(strings.ToLower(page.FullText), q) {
			results = append(results, page)
		}
	}

	return results
}

// DebugMANDiscovery is a noisy diagnostic helper; call manually if needed.
func (mi *MANIndexer) DebugMANDiscovery() {
	color.Cyan("DEBUG: Testing MAN page discovery methods...")

	manPath := mi.getMANPath()
	color.Cyan("MAN Path detected: %s", manPath)

	// Test man -k
	color.Cyan("Testing 'man -k' method...")
	cmd := exec.Command("man", "-k", ".")
	output, err := cmd.Output()
	if err != nil {
		color.Red("'man -k' failed: %v", err)
	} else {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		color.Green("'man -k' found %d entries", len(lines))
		for i := 0; i < min(3, len(lines)); i++ {
			color.Cyan("  Sample %d: %s", i+1, lines[i])
		}
	}

	// Test directory scan
	color.Cyan("Testing directory scanning...")
	paths := strings.Split(manPath, ":")
	totalFiles := 0
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			color.Green("MAN directory exists: %s", p)
			count := mi.countFilesInPath(p)
			color.Cyan("  Contains ~%d files", count)
			totalFiles += count
		} else {
			color.Red("MAN directory missing: %s", p)
		}
	}
	color.Cyan("Total estimated MAN files: %d", totalFiles)
}

func (mi *MANIndexer) countFilesInPath(path string) int {
	count := 0
	for _, cat := range mi.categories {
		categoryPath := filepath.Join(path, "man"+cat)
		entries, err := os.ReadDir(categoryPath)
		if err == nil {
			count += len(entries)
		}
	}
	return count
}

// Common commands that users actually need - EXPANDED LIST
var commonCommands = []string{
	// File operations
	"ls", "cd", "pwd", "cp", "mv", "rm", "mkdir", "rmdir", "touch", "cat",
	"more", "less", "head", "tail", "find", "locate", "which", "whereis",
	"file", "stat", "du", "df", "mount", "umount", "chmod", "chown", "chgrp",
	"ln", "readlink", "realpath", "basename", "dirname", "pathchk", "mktemp",

	// Text processing
	"grep", "egrep", "fgrep", "awk", "sed", "cut", "paste", "sort", "uniq",
	"wc", "tr", "tee", "column", "expand", "unexpand", "fmt", "pr", "nl",
	"fold", "join", "split", "csplit", "tac", "rev", "comm", "diff", "patch",

	// System monitoring
	"ps", "top", "htop", "kill", "pkill", "killall", "jobs", "bg", "fg",
	"nice", "renice", "free", "vmstat", "iostat", "mpstat", "sar", "lsof",
	"netstat", "ss", "uptime", "w", "who", "last", "dmesg", "journalctl",
	"sysctl", "uname", "hostname", "domainname", "dnsdomainname",
	"nisdomainname", "ypdomainname",

	// Network
	"ping", "traceroute", "tracepath", "curl", "wget", "ssh", "scp", "rsync",
	"ftp", "sftp", "ifconfig", "ip", "route", "arp", "dig", "nslookup",
	"whois", "host", "nmap", "nc", "netcat", "telnet", "openssl",
	"ssh-keygen", "ssh-copy-id", "ssh-add", "ssh-agent",

	// Package management & runtimes
	"apt", "apt-get", "apt-cache", "dpkg", "yum", "dnf", "rpm", "brew",
	"pip", "npm", "gem", "cargo", "go", "composer", "apk", "zypper", "pacman",
	"snap", "flatpak", "conda", "port",

	// Development & DevOps
	"git", "svn", "make", "gcc", "g++", "clang", "gdb", "valgrind", "strace",
	"ltrace", "docker", "kubectl", "terraform", "ansible", "puppet", "chef",
	"node", "python", "python3", "ruby", "perl", "php", "java", "javac",
	"mvn", "gradle", "cmake", "autoconf", "automake", "libtool", "pkg-config",

	// Archives
	"tar", "gzip", "gunzip", "bzip2", "bunzip2", "zip", "unzip", "7z", "rar",
	"unrar", "xz", "unxz", "zcat", "bzcat", "xzcat", "ar", "cpio", "dump",
	"restore",

	// User management
	"who", "w", "whoami", "id", "groups", "passwd", "su", "sudo", "useradd",
	"userdel", "usermod", "groupadd", "groupdel", "groupmod", "chage",
	"chsh", "chfn", "newusers", "pwck", "grpck", "lastlog", "faillog",

	// Process/system
	"shutdown", "reboot", "halt", "poweroff", "date", "time", "cal", "bc",
	"echo", "printf", "test", "expr", "sleep", "wait", "timeout", "watch",
	"crontab", "at", "batch", "nohup", "setsid", "screen", "tmux", "script",
	"logger", "wall", "write", "mesg",

	// Shell / core utils
	"alias", "unalias", "export", "unset", "source", "history", "type",
	"help", "man", "info", "whatis", "apropos", "clear", "reset", "tput",
	"stty", "set", "shopt", "ulimit", "umask", "fc", "bind", "complete",
	"compgen", "dirs", "pushd", "popd", "times", "disown", "suspend",

	// Crypto / checksums
	"gpg", "md5sum", "sha1sum", "sha256sum", "sha512sum", "base64", "base32",
	"uuencode", "uudecode",

	// System info / hardware
	"lscpu", "lsblk", "lsusb", "lspci", "lsmod", "modinfo", "modprobe",
	"dmidecode", "hdparm", "smartctl", "fdisk", "parted", "mkfs", "fsck",
	"blkid", "swapon", "swapoff",

	// Editors / viewers
	"vi", "vim", "nano", "emacs", "ed", "ex", "view", "vimdiff", "sdiff",
	"colordiff",

	// Terminal / sessions
	"tty", "script", "screen", "tmux", "byobu", "expect", "dialog", "whiptail",
}

// isUsefulCommand filters commands to those likely to be useful in Helix
func (mi *MANIndexer) isUsefulCommand(command string) bool {
	if len(command) < 2 {
		return false
	}

	for _, ch := range command {
		if !((ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_') {
			return false
		}
	}

	for _, common := range commonCommands {
		if strings.EqualFold(command, common) {
			return true
		}
	}

	usefulPatterns := []string{
		"git-", "docker-", "kubectl-", "aws-", "gcloud-",
		"systemctl", "journalctl", "logrotate", "crontab",
	}

	for _, p := range usefulPatterns {
		if strings.Contains(command, p) {
			return true
		}
	}

	return false
}

// GetAllIndexedPages exposes all pages to the RAG system
func (mi *MANIndexer) GetAllIndexedPages() []MANPage {
	mi.mu.RLock()
	defer mi.mu.RUnlock()

	pages := make([]MANPage, 0, len(mi.indexed))
	for _, page := range mi.indexed {
		pages = append(pages, page)
	}
	return pages
}

// Shared min helper for the rag package (used by DebugMANDiscovery/system.go)
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
