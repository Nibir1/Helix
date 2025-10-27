package utils

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
)

// SyntaxHighlighter handles command parsing and colorization
type SyntaxHighlighter struct {
	colors *SyntaxColors
}

// SyntaxColors holds color configurations for different command components
type SyntaxColors struct {
	Command     *color.Color
	Option      *color.Color
	Flag        *color.Color
	String      *color.Color
	Number      *color.Color
	Path        *color.Color
	Variable    *color.Color
	Comment     *color.Color
	Operator    *color.Color
	Punctuation *color.Color
}

// NewSyntaxHighlighter creates a new syntax highlighter
func NewSyntaxHighlighter() *SyntaxHighlighter {
	return &SyntaxHighlighter{
		colors: &SyntaxColors{
			Command:     color.New(color.FgGreen, color.Bold),
			Option:      color.New(color.FgCyan),
			Flag:        color.New(color.FgYellow),
			String:      color.New(color.FgMagenta),
			Number:      color.New(color.FgBlue),
			Path:        color.New(color.FgWhite, color.Underline),
			Variable:    color.New(color.FgHiBlue),
			Comment:     color.New(color.FgHiBlack),
			Operator:    color.New(color.FgHiRed),
			Punctuation: color.New(color.FgWhite),
		},
	}
}

// HighlightCommand parses and colorizes a shell command
func (sh *SyntaxHighlighter) HighlightCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}

	// Parse the command into tokens
	tokens := sh.tokenizeCommand(command)

	// Colorize each token
	var result strings.Builder
	for _, token := range tokens {
		result.WriteString(sh.colorizeToken(token))
	}

	return result.String()
}

// Token represents a parsed component of a command
type Token struct {
	Type  TokenType
	Value string
}

// TokenType defines the type of command component
type TokenType int

const (
	TokenCommand TokenType = iota
	TokenOption
	TokenFlag
	TokenString
	TokenNumber
	TokenPath
	TokenVariable
	TokenComment
	TokenOperator
	TokenPunctuation
	TokenUnknown
)

// tokenizeCommand breaks down a command into identifiable components
func (sh *SyntaxHighlighter) tokenizeCommand(command string) []Token {
	var tokens []Token
	position := 0
	commandLength := len(command)

	for position < commandLength {
		// Skip whitespace
		if command[position] == ' ' {
			tokens = append(tokens, Token{TokenPunctuation, " "})
			position++
			continue
		}

		// Try to match different token types
		matched := false

		// Match comments
		if !matched && command[position] == '#' {
			comment := sh.extractComment(command[position:])
			tokens = append(tokens, Token{TokenComment, comment})
			position += len(comment)
			matched = true
		}

		// Match strings (single and double quoted)
		if !matched && (command[position] == '"' || command[position] == '\'') {
			str := sh.extractQuotedString(command[position:])
			tokens = append(tokens, Token{TokenString, str})
			position += len(str)
			matched = true
		}

		// Match variables
		if !matched && command[position] == '$' {
			variable := sh.extractVariable(command[position:])
			tokens = append(tokens, Token{TokenVariable, variable})
			position += len(variable)
			matched = true
		}

		// Match paths (starting with /, ./, ../, ~/)
		if !matched && sh.isPathStart(command, position) {
			path := sh.extractPath(command[position:])
			tokens = append(tokens, Token{TokenPath, path})
			position += len(path)
			matched = true
		}

		// Match flags (starting with - or --)
		if !matched && command[position] == '-' {
			flag := sh.extractFlag(command[position:])
			tokenType := TokenFlag
			if strings.HasPrefix(flag, "--") {
				tokenType = TokenOption
			}
			tokens = append(tokens, Token{tokenType, flag})
			position += len(flag)
			matched = true
		}

		// Match numbers
		if !matched && sh.isDigit(command[position]) {
			number := sh.extractNumber(command[position:])
			tokens = append(tokens, Token{TokenNumber, number})
			position += len(number)
			matched = true
		}

		// Match operators (&&, ||, |, >, >>, <, etc.)
		if !matched && sh.isOperator(command[position]) {
			operator := sh.extractOperator(command[position:])
			tokens = append(tokens, Token{TokenOperator, operator})
			position += len(operator)
			matched = true
		}

		// Match punctuation (;, &, etc.)
		if !matched && sh.isPunctuation(command[position]) {
			tokens = append(tokens, Token{TokenPunctuation, string(command[position])})
			position++
			matched = true
		}

		// Match words (commands, arguments, etc.)
		if !matched {
			word := sh.extractWord(command[position:])
			tokenType := sh.classifyWord(word, tokens)
			tokens = append(tokens, Token{tokenType, word})
			position += len(word)
		}
	}

	return tokens
}

// colorizeToken applies color to a token based on its type
func (sh *SyntaxHighlighter) colorizeToken(token Token) string {
	switch token.Type {
	case TokenCommand:
		return sh.colors.Command.Sprint(token.Value)
	case TokenOption:
		return sh.colors.Option.Sprint(token.Value)
	case TokenFlag:
		return sh.colors.Flag.Sprint(token.Value)
	case TokenString:
		return sh.colors.String.Sprint(token.Value)
	case TokenNumber:
		return sh.colors.Number.Sprint(token.Value)
	case TokenPath:
		return sh.colors.Path.Sprint(token.Value)
	case TokenVariable:
		return sh.colors.Variable.Sprint(token.Value)
	case TokenComment:
		return sh.colors.Comment.Sprint(token.Value)
	case TokenOperator:
		return sh.colors.Operator.Sprint(token.Value)
	case TokenPunctuation:
		return sh.colors.Punctuation.Sprint(token.Value)
	default:
		return token.Value
	}
}

// Helper methods for token extraction

func (sh *SyntaxHighlighter) extractComment(input string) string {
	// Comment goes until end of line
	end := strings.Index(input, "\n")
	if end == -1 {
		return input
	}
	return input[:end]
}

func (sh *SyntaxHighlighter) extractQuotedString(input string) string {
	quoteChar := input[0]
	escaped := false
	result := string(quoteChar)

	for i := 1; i < len(input); i++ {
		char := input[i]
		result += string(char)

		if !escaped && char == quoteChar {
			break
		}

		if char == '\\' && !escaped {
			escaped = true
		} else {
			escaped = false
		}
	}

	return result
}

func (sh *SyntaxHighlighter) extractVariable(input string) string {
	// Variables: $VAR, ${VAR}, $(command)
	if len(input) > 1 && input[1] == '{' {
		// ${VAR} format
		end := strings.Index(input, "}")
		if end != -1 {
			return input[:end+1]
		}
	} else if len(input) > 1 && input[1] == '(' {
		// $(command) format
		end := strings.Index(input, ")")
		if end != -1 {
			return input[:end+1]
		}
	} else {
		// $VAR format
		for i := 1; i < len(input); i++ {
			if !sh.isVariableChar(input[i]) {
				return input[:i]
			}
		}
	}
	return input
}

func (sh *SyntaxHighlighter) extractPath(input string) string {
	// Extract path-like strings
	i := 0
	for i < len(input) {
		char := input[i]
		if char == ' ' || char == ';' || char == '&' || char == '|' || char == '>' || char == '<' {
			break
		}
		i++
	}
	return input[:i]
}

func (sh *SyntaxHighlighter) extractFlag(input string) string {
	// Flags: -a, --long-flag, -abc (multiple short flags)
	i := 0
	for i < len(input) {
		char := input[i]
		if char == ' ' || char == '=' || char == ';' || char == '&' {
			break
		}
		i++
	}
	return input[:i]
}

func (sh *SyntaxHighlighter) extractNumber(input string) string {
	i := 0
	decimalPoint := false
	for i < len(input) {
		char := input[i]
		if char >= '0' && char <= '9' {
			i++
		} else if char == '.' && !decimalPoint {
			decimalPoint = true
			i++
		} else {
			break
		}
	}
	return input[:i]
}

func (sh *SyntaxHighlighter) extractOperator(input string) string {
	// Multi-character operators: &&, ||, >>, <<, etc.
	if len(input) >= 2 {
		twoChar := input[:2]
		if twoChar == "&&" || twoChar == "||" || twoChar == ">>" || twoChar == "<<" {
			return twoChar
		}
	}
	return string(input[0])
}

func (sh *SyntaxHighlighter) extractWord(input string) string {
	i := 0
	for i < len(input) {
		char := input[i]
		if char == ' ' || char == ';' || char == '&' || char == '|' || char == '>' || char == '<' {
			break
		}
		i++
	}
	return input[:i]
}

// Helper classification and detection methods

func (sh *SyntaxHighlighter) classifyWord(word string, previousTokens []Token) TokenType {
	// If this is the first token, it's likely a command
	if len(previousTokens) == 0 {
		return TokenCommand
	}

	// Check if previous token was a flag that expects an argument
	if len(previousTokens) > 0 {
		lastToken := previousTokens[len(previousTokens)-1]
		if lastToken.Type == TokenFlag || lastToken.Type == TokenOption {
			// Some flags commonly take arguments that look like values
			if sh.looksLikeFlagArgument(lastToken.Value, word) {
				return TokenString
			}
		}
	}

	// Default to string/argument
	return TokenString
}

func (sh *SyntaxHighlighter) looksLikeFlagArgument(flag, word string) bool {
	// Flags that typically take specific types of arguments
	pathFlags := []string{"--file", "-f", "--path", "-p", "--dir", "-d", "--output", "-o"}
	numberFlags := []string{"--port", "-p", "--timeout", "-t", "--count", "-c", "--limit", "-l"}

	for _, pathFlag := range pathFlags {
		if flag == pathFlag && (strings.Contains(word, "/") || strings.Contains(word, ".")) {
			return true
		}
	}

	for _, numberFlag := range numberFlags {
		if flag == numberFlag && sh.isNumber(word) {
			return true
		}
	}

	return false
}

func (sh *SyntaxHighlighter) isPathStart(input string, position int) bool {
	if position >= len(input) {
		return false
	}

	char := input[position]
	return char == '/' || char == '~' ||
		(char == '.' && position+1 < len(input) && input[position+1] == '/') ||
		(char == '.' && position+2 < len(input) && input[position+1] == '.' && input[position+2] == '/')
}

func (sh *SyntaxHighlighter) isDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

func (sh *SyntaxHighlighter) isOperator(char byte) bool {
	return char == '|' || char == '&' || char == '>' || char == '<' || char == ';'
}

func (sh *SyntaxHighlighter) isPunctuation(char byte) bool {
	return char == ';' || char == '&' || char == '(' || char == ')' || char == '{' || char == '}' || char == '='
}

func (sh *SyntaxHighlighter) isVariableChar(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9') || char == '_'
}

func (sh *SyntaxHighlighter) isNumber(str string) bool {
	if str == "" {
		return false
	}

	// Simple check - all characters are digits
	for _, char := range str {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// PrintHighlightedCommand prints a command with syntax highlighting
func (sh *SyntaxHighlighter) PrintHighlightedCommand(description, command string) {
	if command == "" {
		return
	}

	highlighted := sh.HighlightCommand(command)

	if description != "" {
		fmt.Printf("%s %s\n", color.CyanString("🚀"), color.WhiteString(description))
	}

	fmt.Printf("%s %s\n", color.YellowString("💻 Command:"), highlighted)
}

// ExplainCommandComponents provides a brief explanation of command parts
func (sh *SyntaxHighlighter) ExplainCommandComponents(command string) {
	tokens := sh.tokenizeCommand(command)

	fmt.Println(color.CyanString("📖 Command Breakdown:"))

	for _, token := range tokens {
		if token.Type == TokenPunctuation && token.Value == " " {
			continue
		}

		explanation := sh.getTokenExplanation(token)
		if explanation != "" {
			fmt.Printf("  %s: %s\n",
				sh.colorizeToken(token),
				color.WhiteString(explanation))
		}
	}
}

func (sh *SyntaxHighlighter) getTokenExplanation(token Token) string {
	switch token.Type {
	case TokenCommand:
		return "Main command or executable"
	case TokenOption:
		return "Long option (usually descriptive)"
	case TokenFlag:
		return "Short flag or option"
	case TokenString:
		return "Text argument or value"
	case TokenNumber:
		return "Numeric value"
	case TokenPath:
		return "File or directory path"
	case TokenVariable:
		return "Environment variable"
	case TokenComment:
		return "Comment (ignored by shell)"
	case TokenOperator:
		return "Control operator (pipes, redirection)"
	default:
		return ""
	}
}
