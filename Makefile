# To run the project.
start:
	go run .

# Create a dist directory if it doesn't exist, then build the binary there.

# Build for macOS/Linux
macBuild:
	mkdir -p dist
	go build -o dist/helix

# Build for Windows
winBuild:
	mkdir -p dist
	go build -o dist/helix.exe


.PHONY: start macBuild winBuild