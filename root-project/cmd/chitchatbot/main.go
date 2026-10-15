package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"chitchatbot/internal/bot"
)

func main() {
	var (
		backend  = flag.String("backend", "", "browser backend: rod or chromedp")
		profile  = flag.String("profile", "", "profile name stored under data/profiles/<profile>")
		headless = flag.Bool("headless", false, "run browser headless")
		dataDir  = flag.String("data-dir", "", "project data directory")
	)

	flag.Parse()

	reader := bufio.NewReader(os.Stdin)

	if strings.TrimSpace(*backend) == "" {
		fmt.Print("Choose backend [rod/chromedp] (default: rod): ")
		text, _ := reader.ReadString('\n')
		text = strings.TrimSpace(strings.ToLower(text))
		if text == "" {
			text = "rod"
		}
		*backend = text
	}

	if strings.TrimSpace(*profile) == "" {
		fmt.Print("Profile name (default: default): ")
		text, _ := reader.ReadString('\n')
		text = strings.TrimSpace(text)
		if text == "" {
			text = "default"
		}
		*profile = text
	}

	if strings.TrimSpace(*dataDir) == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to detect working directory:", err)
			os.Exit(1)
		}
		*dataDir = filepath.Join(wd, "data")
	}

	cfg := bot.DefaultConfig(*dataDir, *profile, strings.ToLower(strings.TrimSpace(*backend)), *headless)

	ctx := context.Background()
	if err := bot.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
