package main

import (
	"os"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
