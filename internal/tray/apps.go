package tray

import (
	"fmt"

	"fyne.io/systray"

	"mergelanes/internal/apps"
	"mergelanes/internal/engine"
	"mergelanes/internal/logx"
	"mergelanes/internal/state"
)

// appRows is how many apps the Apps submenu shows: the busiest recent ones, apps with a rule first.
const appRows = 12

// appMenu is the Apps submenu. Each row is one app, and its own submenu picks the app's lane:
// Auto, Merge, Lowest ping, or Only <adapter> for each adapter the menu shows.
//
// Every item is made up front and hidden or shown later: on Windows a hidden item is taken out of
// the menu, so children have to be added while their parent is still in it (see onReady).
type appMenu struct {
	parent, empty *systray.MenuItem
	rows          [appRows]*systray.MenuItem
	auto, merge   [appRows]*systray.MenuItem
	fastest       [appRows]*systray.MenuItem
	only          [appRows][trayRows]*systray.MenuItem

	names    [appRows]string  // the app each row shows
	adapters [trayRows]string // the adapter each Only item is for, the same in every row
	clicks   chan appClick

	lastRows   [appRows]string
	lastOnly   [trayRows]string
	lastMode   [appRows]string
	emptyShown bool
}

type appClick struct {
	row     int
	mode    apps.ModeKind
	adapter int // index into adapters, for apps.ModeAdapter
}

func (m *appMenu) build() {
	m.clicks = make(chan appClick, 4)
	m.parent = systray.AddMenuItem("Apps", "Choose which connection each app uses")
	m.empty = m.parent.AddSubMenuItem("No apps yet: they show up here once they connect", "")
	m.empty.Disable()
	click := func(it *systray.MenuItem, c appClick) {
		go func() {
			for range it.ClickedCh {
				m.clicks <- c
			}
		}()
	}
	for i := range appRows {
		r := m.parent.AddSubMenuItem("", "Choose which connection this app uses")
		m.rows[i] = r
		m.auto[i] = r.AddSubMenuItemCheckbox("Auto", "Games and voice chat on the connection with the lowest ping, everything else merged over all connections", false)
		click(m.auto[i], appClick{row: i, mode: apps.ModeAuto})
		m.merge[i] = r.AddSubMenuItemCheckbox("Merge all connections", "Every connection of this app is spread over all adapters, UDP too (best for torrents and download managers)", false)
		click(m.merge[i], appClick{row: i, mode: apps.ModeMerge})
		m.fastest[i] = r.AddSubMenuItemCheckbox("Lowest ping", "Every connection of this app on the adapter with the lowest ping", false)
		click(m.fastest[i], appClick{row: i, mode: apps.ModeFastest})
		for j := range trayRows {
			it := r.AddSubMenuItemCheckbox("", "Every connection of this app on this adapter; if it fails, on the fastest other one", false)
			m.only[i][j] = it
			click(it, appClick{row: i, mode: apps.ModeAdapter, adapter: j})
			it.Hide()
		}
		r.Hide()
	}
	m.emptyShown = true
}

// refresh shows the recent apps and ticks each one's rule. adapters are the adapters the menu
// lists, in order.
func (m *appMenu) refresh(pol *apps.Policy, adapters []string) {
	if pol == nil || m.parent == nil {
		return
	}
	for j := range trayRows {
		name := ""
		if j < len(adapters) {
			name = adapters[j]
		}
		if name == m.lastOnly[j] {
			continue
		}
		m.adapters[j], m.lastOnly[j] = name, name
		for i := range appRows {
			if name == "" {
				m.only[i][j].Hide()
			} else {
				m.only[i][j].SetTitle("Only " + name)
				m.only[i][j].Show()
			}
		}
		for i := range m.lastMode {
			m.lastMode[i] = "" // re-tick: an Only item now means another adapter
		}
	}

	recent := pol.Recent(apps.RecentWindow, appRows)
	if want := len(recent) == 0; want != m.emptyShown {
		if want {
			m.empty.Show()
		} else {
			m.empty.Hide()
		}
		m.emptyShown = want
	}
	for i := range appRows {
		if i >= len(recent) {
			if m.lastRows[i] != "" {
				m.rows[i].Hide()
				m.lastRows[i], m.names[i], m.lastMode[i] = "", "", ""
			}
			continue
		}
		a := recent[i]
		mode, _ := apps.ParseMode(a.Mode)
		txt := a.Name + "  ·  " + mode.Label()
		if a.Adapter != "" {
			txt += fmt.Sprintf("  (last: %s via %s)", a.Lane, a.Adapter)
		}
		m.names[i] = a.Name
		if txt != m.lastRows[i] {
			m.rows[i].SetTitle(txt)
			m.rows[i].Show()
			m.lastRows[i] = txt
		}
		if a.Mode != m.lastMode[i] {
			m.tick(i, mode)
			m.lastMode[i] = a.Mode
		}
	}
}

func (m *appMenu) tick(row int, mode apps.Mode) {
	set := func(it *systray.MenuItem, want bool) {
		if it.Checked() != want {
			if want {
				it.Check()
			} else {
				it.Uncheck()
			}
		}
	}
	set(m.auto[row], mode.Kind == apps.ModeAuto)
	set(m.merge[row], mode.Kind == apps.ModeMerge)
	set(m.fastest[row], mode.Kind == apps.ModeFastest)
	for j := range trayRows {
		set(m.only[row][j], mode.Kind == apps.ModeAdapter && m.adapters[j] != "" && m.adapters[j] == mode.Adapter)
	}
}

// applyApp sets the rule a click asked for and saves every rule.
func (u *trayUI) applyApp(c appClick) {
	name := u.apps.names[c.row]
	if name == "" || u.pol == nil {
		return
	}
	mode := apps.Mode{Kind: c.mode}
	if c.mode == apps.ModeAdapter {
		if mode.Adapter = u.apps.adapters[c.adapter]; mode.Adapter == "" {
			return
		}
	}
	u.setApp(name, mode)
}

// setApp is a rule from the menu or from `mergelanes app` in a terminal.
func (u *trayUI) setApp(name string, mode apps.Mode) {
	u.pol.SetRule(name, mode)
	if err := state.SaveApps(u.stateDir, u.pol.Rules()); err != nil {
		u.lastErr = "app rule: " + err.Error()
		logx.Printf("error: %v\n", err)
	}
	logx.Printf("%s: %s\n", name, mode.Label())
	for i := range u.apps.lastMode { // re-tick every row now, not on the next refresh
		u.apps.lastMode[i] = ""
	}
	u.apps.refresh(u.pol, u.adapterNames())
}

// adapterNames are the adapters the menu lists, in row order.
func (u *trayUI) adapterNames() []string {
	var out []string
	for _, n := range u.rowNames {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// laneNote marks the adapter that games and voice chat use in its menu row.
func laneNote(s engine.SlotStats, on bool) string {
	txt := ""
	if s.RTTms > 0 {
		txt += fmt.Sprintf("  %.0f ms", s.RTTms)
	}
	if on && s.Primary {
		txt += "  (games)"
	}
	return txt
}
