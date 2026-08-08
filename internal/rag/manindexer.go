// internal/rag/manindexer.go
// Purpose: MAN page discovery, parsing, and in-memory indexing with parallel
// workers feeding the vector store.
// Hardening: IndexAvailableManPages and its workers now accept a context. On
// cancellation the workers drain the discovery channel without exec'ing `man`,
// so the producer never blocks and the whole indexing pipeline unwinds within
// milliseconds of Ctrl+C.
package rag

import (
	"context"
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

type MANIndexer struct {
	env             shell.Env
	indexDir        string
	indexed         map[string]MANPage
	mu              sync.RWMutex
	categories      []string
	discoveredTotal int
	silent          bool
}

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

func (mi *MANIndexer) SetSilent(s bool) { mi.silent = s }

func (mi *MANIndexer) logBlue(format string, args ...interface{}) {
	if !mi.silent {
		color.Blue(format, args...)
	}
}

func (mi *MANIndexer) logCyan(format string, args ...interface{}) {
	if !mi.silent {
		color.Cyan(format, args...)
	}
}

func (mi *MANIndexer) logGreen(format string, args ...interface{}) {
	if !mi.silent {
		color.Green(format, args...)
	}
}

func (mi *MANIndexer) logYellow(format string, args ...interface{}) {
	if !mi.silent {
		color.Yellow(format, args...)
	}
}

func (mi *MANIndexer) logRed(format string, args ...interface{}) {
	if !mi.silent {
		color.Red(format, args...)
	}
}

// IndexAvailableManPages discovers and indexes MAN pages under a caller
// context so Ctrl+C (via the interrupt manager) aborts indexing promptly.
//
// Args:
//   - ctx: cancellation context.
//
// Returns: error (filesystem/index errors; context.Canceled is absorbed by the
// drain-on-cancel workers and surfaces as a fast, clean unwind).
// Complexity: O(number of discovered pages × man exec time) when not cancelled.
func (mi *MANIndexer) IndexAvailableManPages(ctx context.Context) error {
	mi.logBlue("Scanning for MAN pages...")
	if err := mi.ensureIndexDir(); err != nil {
		return fmt.Errorf("failed to create index directory: %w", err)
	}
	manPath := mi.getMANPath()
	mi.logCyan("MAN path: %s", manPath)
	var wg sync.WaitGroup
	pageChan := make(chan string, 100)
	resultChan := make(chan MANPage, 100)
	workerCount := 6
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go mi.manPageWorker(ctx, &wg, pageChan, resultChan)
	}
	go func() {
		wg.Wait()
		close(resultChan)
	}()
	go mi.findMANPages(manPath, pageChan)
	processed := 0
	for page := range resultChan {
		mi.mu.Lock()
		mi.indexed[page.Name] = page
		mi.mu.Unlock()
		processed++
	}
	if processed == 0 {
		mi.logYellow("MAN page indexing finished but no pages were usable")
	} else {
		mi.logGreen("MAN page indexing completed! Indexed %d pages", processed)
	}
	return mi.saveIndex()
}

func (mi *MANIndexer) findMANPages(manPath string, pageChan chan<- string) {
	defer close(pageChan)
	mi.logCyan("Using smart MAN page discovery...")
	totalFound := 0
	methods := []func(chan<- string) int{mi.tryManKEnhanced, mi.tryDirectoryScan}
	for _, method := range methods {
		count := method(pageChan)
		if count > 0 {
			totalFound += count
		}
	}
	mi.discoveredTotal = totalFound
	if totalFound == 0 {
		mi.logRed("No MAN pages found using any discovery method")
		mi.logYellow("MAN pages might not be installed or MANPATH is misconfigured")
	} else {
		mi.logGreen("Discovered %d candidate commands for MAN indexing (filtered to useful ones)", totalFound)
	}
}

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
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		command := parts[0]
		command = strings.Split(command, "(")[0]
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

// manPageWorker consumes discovery candidates and renders MAN pages.
// FIX (interrupt hardening): on cancellation the worker keeps draining the
// channel (so the producer never blocks on a full buffer) but skips the
// expensive `man` exec, letting the pipeline unwind within milliseconds.
func (mi *MANIndexer) manPageWorker(ctx context.Context, wg *sync.WaitGroup, pageChan <-chan string, resultChan chan<- MANPage) {
	defer wg.Done()
	for command := range pageChan {
		if ctx.Err() != nil {
			continue // drain fast, do no work
		}
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

func (mi *MANIndexer) processMANPage(command string) (MANPage, error) {
	cmd := exec.Command("man", command)
	output, err := cmd.Output()
	if err != nil {
		return MANPage{}, fmt.Errorf("failed to get MAN page for %s: %w", command, err)
	}
	content := string(output)
	return mi.parseMANContent(command, content), nil
}

func (mi *MANIndexer) parseMANContent(command, content string) MANPage {
	page := MANPage{Name: command, FullText: content}
	lines := strings.Split(content, "\n")
	var currentSection string
	var sectionContent strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.ToUpper(line) == line && len(line) > 0 && !strings.Contains(line, " ") {
			mi.processSection(currentSection, sectionContent.String(), &page)
			currentSection = line
			sectionContent.Reset()
			continue
		}
		sectionContent.WriteString(line + "\n")
	}
	mi.processSection(currentSection, sectionContent.String(), &page)
	if page.Description == "" {
		page.Description = mi.extractDescription(content)
	}
	return page
}

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

func (mi *MANIndexer) ensureIndexDir() error { return os.MkdirAll(mi.indexDir, 0o755) }

func (mi *MANIndexer) saveIndex() error {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	mi.logGreen("MAN page index ready (%d pages)", len(mi.indexed))
	return nil
}

func (mi *MANIndexer) GetIndexedCount() int {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	return len(mi.indexed)
}

func (mi *MANIndexer) GetPage(name string) (MANPage, bool) {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	page, ok := mi.indexed[name]
	return page, ok
}

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

func (mi *MANIndexer) DebugMANDiscovery() {
	mi.logCyan("DEBUG: Testing MAN page discovery methods...")
	manPath := mi.getMANPath()
	mi.logCyan("MAN Path detected: %s", manPath)
	mi.logCyan("Testing 'man -k' method...")
	cmd := exec.Command("man", "-k", ".")
	output, err := cmd.Output()
	if err != nil {
		mi.logRed("'man -k' failed: %v", err)
	} else {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		mi.logGreen("'man -k' found %d entries", len(lines))
		for i := 0; i < min(3, len(lines)); i++ {
			mi.logCyan("  Sample %d: %s", i+1, lines[i])
		}
	}
	mi.logCyan("Testing directory scanning...")
	paths := strings.Split(manPath, ":")
	totalFiles := 0
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			mi.logGreen("MAN directory exists: %s", p)
			count := mi.countFilesInPath(p)
			mi.logCyan("  Contains ~%d files", count)
			totalFiles += count
		} else {
			mi.logRed("MAN directory missing: %s", p)
		}
	}
	mi.logCyan("Total estimated MAN files: %d", totalFiles)
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

var commonCommands = []string{
	"ls", "cd", "pwd", "cp", "mv", "rm", "mkdir", "rmdir", "touch", "cat",
	"more", "less", "head", "tail", "find", "locate", "which", "whereis",
	"file", "stat", "du", "df", "mount", "umount", "chmod", "chown", "chgrp",
	"ln", "readlink", "realpath", "basename", "dirname", "pathchk", "mktemp",
	"grep", "egrep", "fgrep", "awk", "sed", "cut", "paste", "sort", "uniq",
	"wc", "tr", "tee", "column", "expand", "unexpand", "fmt", "pr", "nl",
	"fold", "join", "split", "csplit", "tac", "rev", "comm", "diff", "patch",
	"ps", "top", "htop", "kill", "pkill", "killall", "jobs", "bg", "fg",
	"nice", "renice", "free", "vmstat", "iostat", "mpstat", "sar", "lsof",
	"netstat", "ss", "uptime", "w", "who", "last", "dmesg", "journalctl",
	"sysctl", "uname", "hostname", "domainname", "dnsdomainname",
	"nisdomainname", "ypdomainname",
	"ping", "traceroute", "tracepath", "curl", "wget", "ssh", "scp", "rsync",
	"ftp", "sftp", "ifconfig", "ip", "route", "arp", "dig", "nslookup",
	"whois", "host", "nmap", "nc", "netcat", "telnet", "openssl",
	"ssh-keygen", "ssh-copy-id", "ssh-add", "ssh-agent",
	"apt", "apt-get", "apt-cache", "dpkg", "yum", "dnf", "rpm", "brew",
	"pip", "npm", "gem", "cargo", "go", "composer", "apk", "zypper", "pacman",
	"snap", "flatpak", "conda", "port",
	"git", "svn", "make", "gcc", "g++", "clang", "gdb", "valgrind", "strace",
	"ltrace", "docker", "kubectl", "terraform", "ansible", "puppet", "chef",
	"node", "python", "python3", "ruby", "perl", "php", "java", "javac",
	"mvn", "gradle", "cmake", "autoconf", "automake", "libtool", "pkg-config",
	"tar", "gzip", "gunzip", "bzip2", "bunzip2", "zip", "unzip", "7z", "rar",
	"unrar", "xz", "unxz", "zcat", "bzcat", "xzcat", "ar", "cpio", "dump",
	"restore",
	"who", "w", "whoami", "id", "groups", "passwd", "su", "sudo", "useradd",
	"userdel", "usermod", "groupadd", "groupdel", "groupmod", "chage",
	"chsh", "chfn", "newusers", "pwck", "grpck", "lastlog", "faillog",
	"shutdown", "reboot", "halt", "poweroff", "date", "time", "cal", "bc",
	"echo", "printf", "test", "expr", "sleep", "wait", "timeout", "watch",
	"crontab", "at", "batch", "nohup", "setsid", "screen", "tmux", "script",
	"logger", "wall", "write", "mesg",
	"alias", "unalias", "export", "unset", "source", "history", "type",
	"help", "man", "info", "whatis", "apropos", "clear", "reset", "tput",
	"stty", "set", "shopt", "ulimit", "umask", "fc", "bind", "complete",
	"compgen", "dirs", "pushd", "popd", "times", "disown", "suspend",
	"gpg", "md5sum", "sha1sum", "sha256sum", "sha512sum", "base64", "base32",
	"uuencode", "uudecode",
	"lscpu", "lsblk", "lsusb", "lspci", "lsmod", "modinfo", "modprobe",
	"dmidecode", "hdparm", "smartctl", "fdisk", "parted", "mkfs", "fsck",
	"blkid", "swapon", "swapoff",
	"vi", "vim", "nano", "emacs", "ed", "ex", "view", "vimdiff", "sdiff",
	"colordiff",
	"tty", "script", "screen", "tmux", "byobu", "expect", "dialog", "whiptail",
}

func (mi *MANIndexer) isUsefulCommand(command string) bool {
	if len(command) < 2 {
		return false
	}
	for _, ch := range command {
		if (ch < 'a' || ch > 'z') &&
			(ch < 'A' || ch > 'Z') &&
			(ch < '0' || ch > '9') &&
			ch != '-' && ch != '_' {
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

func (mi *MANIndexer) GetAllIndexedPages() []MANPage {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	pages := make([]MANPage, 0, len(mi.indexed))
	for _, page := range mi.indexed {
		pages = append(pages, page)
	}
	return pages
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
