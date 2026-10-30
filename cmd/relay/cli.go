package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"relay/internal/browser"
	"relay/internal/client"
	"relay/internal/config"
	"relay/internal/proto"
)

func cmdKeys() int {
	cl, err := client.Connect(true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "relay:", err)
		return 1
	}
	if cl.Spawned {
		fmt.Println("Backend started in the background (stop it with: relay stop)")
	}
	page := cl.Base + "/ui/keys"
	full := page + "#token=" + cl.Token
	if err := browser.Open(full); err != nil {
		fmt.Println("Could not open the browser. Open it manually:")
		fmt.Println(full)
		return 0
	}
	fmt.Println("Opened the keys page:", page)
	return 0
}

func cmdStatus() int {
	cl, err := client.Connect(false)
	if err != nil {
		fmt.Println("The backend is not running. Start the interface with: relay")
		return 1
	}
	st, err := cl.Status()
	if err != nil {
		fmt.Fprintln(os.Stderr, "relay:", err)
		return 1
	}
	fmt.Printf("Relay %s · backend %s · folder %s\n\n", st.Version, cl.Base, st.Workspace)

	fmt.Println("Modes:")
	for _, m := range st.Modes {
		fmt.Printf("  %-8s %s\n", m.Name, strings.Join(m.Models, " → "))
	}

	fmt.Println("\nProviders:")
	for _, p := range st.Providers {
		state := ""
		switch {
		case !p.NeedsKey && p.Keys == 0:
			state = "local, no key needed"
		case p.Keys == 0:
			state = "no key (relay keys)"
		default:
			state = fmt.Sprintf("keys: %d (available %d)", p.Keys, p.KeysAvailable)
		}
		fmt.Printf("  %-12s %-10s %s\n", p.Name, p.Kind, state)
	}

	if len(st.Processes) > 0 {
		fmt.Println("\nProcesses:")
		for _, p := range st.Processes {
			state := p.State
			if p.External {
				state = "running (external)"
			}
			fmt.Printf("  %-12s %s\n", p.Name, state)
		}
	}
	printUsage(st.Usage)
	return 0
}

func printUsage(u map[string]proto.UsageEntry) {
	if len(u) == 0 {
		return
	}
	names := make([]string, 0, len(u))
	for n := range u {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Println("\nUsage:")
	for _, n := range names {
		e := u[n]
		fmt.Printf("  %-14s requests %-5d tokens %d / %d  $%.4f\n", n, e.Requests, e.In, e.Out, e.Cost)
	}
}

func cmdStop() int {
	cl, err := client.Connect(false)
	if err != nil {
		fmt.Println("The backend is not running")
		return 0
	}
	cl.Shutdown()
	fmt.Println("Backend stopped")
	return 0
}

func cmdConfig(args []string) int {
	if len(args) > 0 && args[0] == "--example" {
		fmt.Print(config.ExampleTOML)
		return 0
	}
	home, err := config.Home()
	if err != nil {
		fmt.Fprintln(os.Stderr, "relay:", err)
		return 1
	}
	path, err := config.Ensure(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "relay:", err)
		return 1
	}
	fmt.Println(path)
	return 0
}
