package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

const moduleUsage = `usage:
  hive module                      installed modules
  hive module browse               what the indexes list: trust, the read behind it, what each needs
  hive module add <name>[@sha]     install from an index (a warden checks it); a git URL installs unlisted (you decide)
  hive module rm <name>            uninstall it, with its program's home
  hive module index                the indexes modules install from
  hive module index add|rm <url>   add another index (yours, a friend's) or drop one
`

func cmdModule(args []string) int {
	if len(args) == 0 {
		return withClient(func(c *api.Client) error {
			var st api.State
			if err := c.Do("GET", "/api/state", nil, &st); err != nil {
				return err
			}
			for _, m := range st.Modules {
				fmt.Printf("%-14s %-10s %-10s %s\n", m.Name, orDash(m.Trust), orDash(m.Release), m.Description)
			}
			return nil
		})
	}
	switch {
	case args[0] == "browse" && len(args) == 1:
		return withClient(func(c *api.Client) error {
			var idx struct {
				Modules []modules.Listing
				Errors  []string
			}
			if err := c.Do("GET", "/api/module/index", nil, &idx); err != nil {
				return err
			}
			for _, l := range idx.Modules {
				fmt.Printf("%s  (%s, %s@%s)\n  %s\n  needs: %s\n  read: %s\n", l.Name, l.Trust, l.Repo, l.Sha[:12], l.Description, l.Needs, orDash(l.Read))
				if l.Pulled != "" {
					fmt.Printf("  PULLED: %s\n", l.Pulled)
				}
			}
			for _, e := range idx.Errors {
				fmt.Fprintln(os.Stderr, "unread:", e)
			}
			return nil
		})
	case args[0] == "add" && len(args) == 2:
		return withClient(func(c *api.Client) error {
			var msg string
			if err := c.Do("POST", "/api/module/add", map[string]string{"spec": args[1]}, &msg); err != nil {
				return err
			}
			fmt.Println(msg)
			return nil
		})
	case (args[0] == "rm" || args[0] == "remove") && len(args) == 2:
		return withClient(func(c *api.Client) error {
			var msg string
			if err := c.Do("POST", "/api/module/remove", map[string]string{"module": args[1]}, &msg); err != nil {
				return err
			}
			fmt.Println(msg)
			return nil
		})
	case args[0] == "index" && len(args) == 1:
		ws, err := workspace()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for _, i := range modules.Indexes(ws.LoadConfig()) {
			fmt.Println(i)
		}
		return 0
	case args[0] == "index" && len(args) == 3 && (args[1] == "add" || args[1] == "rm"):
		body := map[string]string{"add": args[2]}
		if args[1] == "rm" {
			body = map[string]string{"remove": args[2]}
		}
		return withClient(func(c *api.Client) error {
			var idx []string
			if err := c.Do("POST", "/api/module/indexes", body, &idx); err != nil {
				return err
			}
			fmt.Println("Modules install from: " + strings.Join(idx, ", "))
			return nil
		})
	}
	fmt.Fprint(os.Stderr, strings.ReplaceAll(moduleUsage, "hive ", core.Cmd+" "))
	return 2
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
