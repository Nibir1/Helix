# Helix 🤖
**An Intelligent, AI-Powered CLI Assistant That Understands Natural Language & System Documentation**

![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)
![License](https://img.shields.io/badge/License-MIT-blue.svg)
![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)

Helix is a revolutionary **AI-powered command-line assistant** that bridges the gap between human language and system commands. Built for developers, sysadmins, and IT enthusiasts, it combines **local AI inference, RAG intelligence, and deep system knowledge** to execute commands safely, efficiently, and intelligently.

---

## 🚀 Why Helix Matters
Helix is more than a CLI tool — it's a showcase of **modern AI systems engineering**:  
- **RAG-Enhanced Intelligence**: Semantic search across 900+ system command docs  
- **Direct llama.cpp Bindings**: Maximum performance with local AI inference  
- **Cross-Platform Mastery**: Works on Windows, Linux, macOS with smart package management  
- **Safety-First Execution**: Multi-layer validation, sandboxing, dry-run modes  

Helix demonstrates skills in **Go**, **AI model integration**, **vector databases**, and **CLI UX design** — exactly the expertise IT recruiters want to see in a portfolio project.

---

## ✨ Technical Highlights

### 🧠 AI & RAG
- **Natural Language to Shell Commands** — `/cmd "find large files older than 30 days"`  
- **Smart Explanations** — `/explain <command>` gives detailed usage with examples  
- **Q&A Intelligence** — `/ask` for system, programming, and DevOps questions  
- **Local Inference Only** — privacy-focused, fully offline using optimized LLaMA models  
- **RAG System** — builds vector store from 450+ commands for semantic retrieval  

### 🔥 llama.cpp Integration
- Direct bindings for **raw performance**  
- Memory-efficient F16 & 4-bit GGUF model loading  
- No external AI dependencies — fully offline capable  

### 🛡️ Safety & Reliability
- **Directory Sandbox**: Restrict execution to safe paths  
- **Dangerous Command Blocking**: Detects 20+ harmful patterns  
- **Dry-Run Mode**: Preview commands before execution  
- **Automatic Quote & Syntax Fixing**: Corrects malformed AI-generated commands  

### ⚡ Git & Package Management
- **Natural Language Git Operations** — `/git "merge feature-branch with squash"`  
- **Cross-Platform Package Support** — apt, brew, choco, winget, pacman, yum, dnf, snap  
- **Batch Operations & Smart Detection** — automates updates and installs  

### 🎨 Professional Terminal UX
- Color-coded syntax highlighting  
- Animated typing effects  
- Command breakdowns & interactive progress indicators  

---

## 🏗️ Architecture Overview
```
Helix/
├── 🧠 RAG System/          # Command indexing, vector store & semantic search
├── 🤖 AI Core/             # llama.cpp integration & prompt engineering
├── ⚡ Command System/      # Safe command execution, Git, package management
├── 🎨 User Experience/     # Terminal enhancements, syntax highlighting
└── 🔧 Utilities/           # Shell detection, configuration, validation
```

---

## 🚀 Quick Start

### Prerequisites
- Go 1.25+  
- 4 GB+ RAM for AI inference  
- 2 GB+ Disk for model storage  
- macOS/Linux/Windows shell  

### Installation

```bash
git clone https://github.com/Nibir1/Helix.git
cd Helix
make current        # Recommended build
./dist/helix
```

Or development build:
```bash
make start
```

---

## 🎯 Example Usage
```bash
# Convert English to shell commands
/cmd "list all files sorted by size"

/explain "git merge --squash feature-branch"

/ask "how do I set up a reverse proxy with nginx?"  

# Package Management
/install git
/update python
/remove nodejs

# Git Operations
/git "undo last commit but keep changes"
/git "clean all untracked files"
```

---

## 🛡️ Safety Features
- Multi-layer validation pipeline  
- Sandbox & restricted directories  
- Dangerous command detection & dry-run previews  
- Automatic syntax & quote correction  

---

## 🧩 Supported Platforms
| Platform | Shells | Package Managers |
|-----------|---------|------------------|
| Windows   | PowerShell, CMD, Git Bash | Chocolatey, Winget, Scoop |
| Linux     | Bash, Zsh, Fish           | apt, yum, dnf, pacman, snap |
| macOS     | Bash, Zsh, Fish           | Homebrew, MacPorts |

---

## 🧠 AI Model Info
- **Default**: TinyLlama-1.1B-Chat GGUF (~700MB, fast)  
- **Optional**: LLaMA-2-7B-Chat GGUF (~4GB, high-quality)  
- **Local Inference**: Offline, privacy-friendly  
- **Automatic Download**: First-run model retrieval  

---

## 🤝 Contributing
- Clone repo & run `make dev`  
- Test features via `/test-ai`  
- Build platform-specific: `make macos/linux/windows`  

---

## 📄 License
MIT License — see LICENSE  
*Note: LLaMA models have separate licenses*  

---

## 🙏 Acknowledgments
- [go-llama.cpp](https://github.com/go-skynet/go-llama.cpp) — High-performance Go bindings  
- [TheBloke](https://huggingface.co/TheBloke) — Quantized GGUF models  
- Meta AI — LLaMA foundation models  
- Contributors helping CLI accessibility ❤️  

---

## 💡 Developer Spotlight
**Nahasat Nibir** — Passionate about building intelligent, high-performance developer tools and AI-powered systems in **Go**, **Python**, and modern system stacks.  
- Portfolio: [GitHub](https://github.com/Nibir1)  
- LinkedIn: [LinkedIn](https://www.linkedin.com/in/nibir-1/)  
- ArtStation: [ArtStation](https://www.artstation.com/nibir) 

---

<div align="center">
**Helix — Making the command line accessible and intelligent through AI.**  

[🐞 Report Bug](https://github.com/Nibir1/Helix/issues) · [💡 Request Feature](https://github.com/Nibir1/Helix/issues) · ⭐ Star the Project
</div>
