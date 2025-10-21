# Helix 🤖  
**An Intelligent, AI-Powered CLI Assistant That Understands Natural Language**

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](https://github.com/Nibir1/Helix)

Helix is a revolutionary command-line interface that bridges the gap between human language and shell commands. Using **local AI inference**, it understands your intent and executes commands safely and efficiently.

---

## ✨ Features

### 🧠 AI-Powered Intelligence
- **Natural Language Processing** — Converts English instructions to shell commands  
- **Local AI Model** — Privacy-focused, offline-capable inference using LLaMA 2  
- **Smart Context Awareness** — Understands your OS, shell, and environment  
- **Command Explanations** — Learn what commands do before executing them  

### 🛡️ Safety First
- **Dangerous Command Detection** — Blocks potentially harmful operations  
- **Confirmation Prompts** — Asks before executing risky commands  
- **Dry-Run Mode** — Preview commands without execution  
- **Command Validation** — Sanitizes and validates AI output  

### 📦 Package Management
- **Cross-Platform Support** — Works with `apt`, `brew`, `choco`, `winget`, `pacman`  
- **Smart Installation** — Automatically uses the right package manager  
- **Version Checking** — Verifies installed packages and suggests updates  
- **Batch Operations** — Install, update, and remove packages effortlessly  

### 🌐 Connectivity
- **Online/Offline Modes** — Adapts based on internet availability  
- **Model Auto-Download** — Automatically downloads AI model on first run  
- **Fallback Modes** — Graceful degradation when the model is unavailable  

### 🎨 Enhanced UX
- **Beautiful Terminal UI** — Colorized output with emojis and formatting  
- **Typing Effects** — Animated AI responses for better experience  
- **Command History** — Persistent history across sessions  
- **Progress Indicators** — Visual feedback for long operations  

---

## 🚀 Quick Start

### Prerequisites
- **Go 1.25+**
- **4 GB+ RAM** (for AI inference)
- **2 GB+ Disk Space** (for model storage)

### 🧩 Installation

#### **Method 1: Download Pre-built Binary**
```bash
# Download from GitHub Releases
# Extract and run directly
./helix
```

#### **Method 2: Build from Source**
```bash
# Clone the repository
git clone https://github.com/Nibir1/Helix.git
cd Helix

# Build for your platform
make current

# Or build for all platforms
make build-all
```

#### **Method 3: Using Go**
```bash
go install github.com/Nibir1/Helix@latest
```

---

## 🎯 Usage

### **Basic Commands**
```bash
# Ask natural language questions
/ask "how do I check disk space on Linux?"

# Generate and execute commands from English
/cmd "list all the files in this directory"

# Explain what a command does
/explain "rm -rf node_modules"

# Install packages intelligently
/install git

# Update existing packages
/update python

# Remove packages
/remove nodejs

# Test the /ask AI feature
/test-ai
```

### **Advanced Features**
```bash
# Toggle dry-run mode (preview commands without execution)
/dry-run

# Check internet connectivity
/online

# View debug information
/debug

# Show help
/help

# Exit Helix
/exit
```

---

## 🛠️ Command Reference

| Command | Syntax | Description |
|----------|---------|-------------|
| `/ask` | `/ask <question>` | Ask general questions |
| `/cmd` | `/cmd <instruction>` | Convert natural language to commands |
| `/explain` | `/explain <command>` | Explain shell commands |
| `/install` | `/install <package>` | Install software packages |
| `/update` | `/update <package>` | Update installed packages |
| `/remove` | `/remove <package>` | Remove packages |
| `/debug` | `/debug` | Show system information |
| `/help` | `/help` | Display help message |
| `/exit` | `/exit` | Exit the application |
| `/test-ai` | `/test-ai` | Test the /ask AI feature |

---

## 🔧 Configuration

Helix automatically configures itself on first run:

```
Model Directory: ~/.helix/models/
Configuration:   ~/.helix/config.json
History File:    ~/.helix_history
```

### **Environment Variables**
```bash
# Custom model directory
export HELIX_MODEL_DIR="/path/to/your/models"

# Disable colored output
export NO_COLOR=1
```

---

## 🧩 Supported Platforms & Package Managers

| Platform | Shells | Package Managers |
|-----------|---------|------------------|
| **Windows** | PowerShell, CMD, Git Bash | Chocolatey, Winget, Scoop |
| **Linux** | Bash, Zsh, Fish | apt, yum, dnf, pacman, snap |
| **macOS** | Bash, Zsh, Fish | Homebrew, MacPorts |

---

## 🧠 AI Model Information

Helix uses the **LLaMA 2 7B Chat GGUF** model:

- **Model**: `llama-2-7b-chat.Q4_0.gguf`  
- **Size**: ~4 GB (quantized)  
- **Source**: [TheBloke/Llama-2-7B-Chat-GGUF](https://huggingface.co/TheBloke/Llama-2-7B-Chat-GGUF)  
- **License**: Custom (see model card)

The model is automatically downloaded on first run and stored locally for offline use.

---

## 🏗️ Architecture
```
Helix/
├── main.go              # CLI entry point & handlers
├── model.go             # AI model integration (llama.cpp)
├── shell.go             # OS & shell detection
├── prompt.go            # AI prompt engineering
├── execute.go           # Command execution & safety
├── pkgmanager.go        # Package management
├── download.go          # Model download with progress
├── history.go           # Command history persistence
├── ux.go                # User experience enhancements
├── utils.go             # Utility functions
├── config.go            # Configuration management
└── go.mod               # Dependencies
```

---

## 🚨 Safety Features

Helix includes multiple layers of protection:

- **Command Sanitization** — Removes dangerous characters and patterns  
- **Pattern Blocking** — Detects and blocks known dangerous commands  
- **User Confirmation** — Prompts before executing potentially risky operations  
- **Dry-Run Mode** — Preview mode for command verification  
- **Execution Limits** — Restricted to safe system operations  

---

## 🐛 Troubleshooting

### **Model Download Fails**
```bash
# Check internet connection
/online

# Manual download from:
# https://huggingface.co/TheBloke/Llama-2-7B-Chat-GGUF
```

### **Command Not Executing**
- Enable dry-run mode to see what would be executed  
- Check command explanations for understanding  
- Verify you have necessary permissions  

### **Performance Issues**
- Ensure sufficient RAM (4 GB+ recommended)  
- Close other memory-intensive applications  
- Use simpler prompts for faster responses  

### **Getting Help**
- Check debug information: `/debug`  
- Review this README for usage instructions  
- Check existing GitHub issues  
- Create a new issue with debug output  

---

## 🤝 Contributing

We welcome contributions!  
Please see our **Contributing Guide** for details.

### **Development Setup**
```bash
git clone https://github.com/Nibir1/Helix.git
cd Helix
go mod download
make dev
```

### **Building**
```bash
# Development build
make dev

# Platform-specific builds
make macos
make linux
make windows

# Build all platforms
make build-all
```

---

## 📄 License

This project is licensed under the **MIT License** — see the [LICENSE](LICENSE) file for details.  
> **Note:** The LLaMA 2 model is subject to its own license terms.  
> Please review the model license at the [Hugging Face repository](https://huggingface.co/TheBloke/Llama-2-7B-Chat-GGUF).

---

## 🙏 Acknowledgments

- [**go-llama.cpp**](https://github.com/go-skynet/go-llama.cpp) — Go bindings for llama.cpp  
- [**TheBloke**](https://huggingface.co/TheBloke) — Quantized GGUF models  
- **Meta AI** — LLaMA 2 model  
- All contributors and testers ❤️  

---

<div align="center">

**Helix — Making the command line accessible to everyone through AI.**

[🐞 Report Bug](https://github.com/Nibir1/Helix/issues) · [💡 Request Feature](https://github.com/Nibir1/Helix/issues)

</div>
