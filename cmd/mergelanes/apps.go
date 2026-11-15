package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"mergelanes/internal/apps"
	"mergelanes/internal/platform"
	"mergelanes/internal/state"
)

// cmdApps lists the apps seen recently with their rule and where their last flow went, then the
// saved rules of apps that have not been seen.
func cmdApps(args []string) {
	fs := flag.NewFlagSet("apps", flag.ExitOnError)
	dir := fs.String("state-dir", state.DefaultDir(), "state directory")
	fs.Parse(args)
	rules, err := state.LoadApps(*dir)
	if err != nil {
		die(err)
	}
	st, on := state.Read(*dir)
	seen := map[string]bool{}
	if on && len(st.Apps) > 0 {
		fmt.Printf("Apps seen in the last %s:\n", apps.RecentWindow)
		for _, a := range st.Apps {
			seen[apps.Key(a.Name)] = true
			m, _ := apps.ParseMode(a.Mode)
			line := fmt.Sprintf("  %-34s %-16s %5d flows", a.Name, m.Label(), a.Flows)
			if a.Adapter != "" {
				line += "   last: " + a.Lane + " via " + a.Adapter
			}
			fmt.Println(line)
		}
	} else if !on {
		fmt.Println("MergeLanes is not running, so no apps have been seen.")
	}
	var rest []string
	for k := range rules {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	if len(rest) > 0 {
		fmt.Println("Saved rules:")
		for _, k := range rest {
			fmt.Printf("  %-34s %s\n", k, rules[k].Label())
		}
	}
	if len(rules) == 0 {
		fmt.Println("No rules: every app is on Auto (games and voice chat on the lowest-ping adapter, the rest merged).")
	}
}

// cmdApp sets one app's rule: `mergelanes app steam.exe merge`, `... fastest`, `... Ethernet`.
func cmdApp(args []string) {
	fs := flag.NewFlagSet("app", flag.ExitOnError)
	dir := fs.String("state-dir", state.DefaultDir(), "state directory")
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprintln(os.Stderr, "usage: mergelanes app <app> auto|merge|fastest|<adapter name>\n"+
			"  auto     games and voice on the lowest-ping adapter, the rest merged (the default)\n"+
			"  merge    every connection spread over all adapters\n"+
			"  fastest  every connection on the lowest-ping adapter\n"+
			"  <name>   every connection on that adapter, the fastest other one if it fails\n"+
			"           (write adapter:<name> for an adapter called auto, merge or fastest)")
		os.Exit(2)
	}
	name := strings.TrimSpace(rest[0])
	if apps.Key(name) == "" {
		die(fmt.Errorf("no app name given"))
	}
	m, err := modeArg(rest[1])
	if err != nil {
		die(err)
	}
	running := platform.InstanceRunning()
	if !running && saveElevated(append([]string{"app"}, args...)) {
		return
	}
	live, err := appApply(*dir, name, m, running, limitWait)
	if err != nil {
		die(err)
	}
	if live {
		fmt.Printf("%s: %s. New connections use it now; open ones stay where they are.\n", name, m.Label())
	} else {
		fmt.Printf("%s: %s. It takes effect the next time MergeLanes starts.\n", name, m.Label())
	}
}

// modeArg reads the mode as typed: a keyword, or any other word as an adapter name.
func modeArg(s string) (apps.Mode, error) {
	v := strings.TrimSpace(s)
	switch strings.ToLower(v) {
	case "auto", "merge", "fastest":
		return apps.ParseMode(strings.ToLower(v))
	case "ping", "lowest-ping":
		return apps.Mode{Kind: apps.ModeFastest}, nil
	case "":
		return apps.Mode{}, fmt.Errorf("no mode given")
	}
	if strings.HasPrefix(v, "adapter:") {
		return apps.ParseMode(v)
	}
	return apps.Mode{Kind: apps.ModeAdapter, Adapter: v}, nil
}

// appApply saves a rule, through the running instance if there is one (it applies it and saves
// apps.json), or straight into apps.json. It reports whether a running instance applied it.
func appApply(dir, name string, m apps.Mode, running bool, wait time.Duration) (live bool, err error) {
	k := apps.Key(name)
	if !running {
		rules, err := state.LoadApps(dir)
		if err != nil {
			return false, err
		}
		if m.Kind == apps.ModeAuto {
			delete(rules, k)
		} else {
			rules[k] = m
		}
		return false, state.SaveApps(dir, rules)
	}
	if err := state.SendApp(dir, state.AppRequest{App: name, Mode: m.String()}); err != nil {
		return true, err
	}
	deadline := time.Now().Add(wait)
	for {
		if rules, err := state.LoadApps(dir); err == nil {
			got, set := rules[k]
			if (m.Kind == apps.ModeAuto && !set) || (set && got == m) {
				return true, nil
			}
		}
		if time.Now().After(deadline) {
			return true, fmt.Errorf("sent the rule, but the running instance did not apply it")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
