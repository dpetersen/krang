package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dpetersen/krang/internal/workspace"
	"github.com/sahilm/fuzzy"
)

type pickerItemKind int

const (
	pickerItemSet  pickerItemKind = iota
	pickerItemRepo
)

type pickerItem struct {
	Kind    pickerItemKind
	Name    string
	Members []string // only for sets
	Checked bool
}

type repoPicker struct {
	title   string
	items   []pickerItem
	cursor  int // index into visibleIndices
	styles  Styles
	theme   Theme
	filter  textinput.Model
	filtering bool
	visibleIndices []int // maps visible position → items index
}

// newRepoPicker builds a picker from sets and the full repo list.
// Sets appear first as group headers, then all individual repos
// are listed (including those in sets) so they can be toggled
// independently.
func newRepoPicker(title string, sets map[string][]string, allRepos []string, styles Styles) repoPicker {
	var items []pickerItem

	// Sort set names for stable ordering.
	var setNames []string
	for name := range sets {
		setNames = append(setNames, name)
	}
	sort.Strings(setNames)

	for _, setName := range setNames {
		members := sets[setName]
		items = append(items, pickerItem{
			Kind:    pickerItemSet,
			Name:    setName,
			Members: members,
		})
	}

	// All repos appear as individual items.
	for _, repo := range allRepos {
		items = append(items, pickerItem{
			Kind: pickerItemRepo,
			Name: repo,
		})
	}

	fi := textinput.New()
	fi.Placeholder = "search repos..."
	fi.CharLimit = 60

	p := repoPicker{
		title:  title,
		items:  items,
		styles: styles,
		theme:  styles.theme,
		filter: fi,
	}
	p.refilter()
	return p
}

// refilter rebuilds visibleIndices based on the current filter text.
func (p *repoPicker) refilter() {
	query := strings.TrimSpace(p.filter.Value())
	if query == "" {
		p.visibleIndices = make([]int, len(p.items))
		for i := range p.items {
			p.visibleIndices[i] = i
		}
		return
	}

	// Build searchable strings: for repos just the name, for sets
	// the set name plus all member names so "terraform" matches a
	// set whose member is called "terraform".
	var searchTargets []string
	for _, item := range p.items {
		if item.Kind == pickerItemSet {
			searchTargets = append(searchTargets,
				item.Name+" "+strings.Join(item.Members, " "))
		} else {
			searchTargets = append(searchTargets, item.Name)
		}
	}

	matches := fuzzy.Find(query, searchTargets)
	p.visibleIndices = make([]int, len(matches))
	for i, m := range matches {
		p.visibleIndices[i] = m.Index
	}

	// Clamp cursor.
	if p.cursor >= len(p.visibleIndices) {
		p.cursor = len(p.visibleIndices) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// sourceIndex returns the items index for the current cursor position,
// or -1 if nothing is visible.
func (p *repoPicker) sourceIndex() int {
	if p.cursor < 0 || p.cursor >= len(p.visibleIndices) {
		return -1
	}
	return p.visibleIndices[p.cursor]
}

func (p *repoPicker) toggle() {
	idx := p.sourceIndex()
	if idx < 0 {
		return
	}

	item := &p.items[idx]
	item.Checked = !item.Checked

	// Toggling a set toggles all its members in other items too.
	if item.Kind == pickerItemSet {
		memberSet := make(map[string]bool)
		for _, m := range item.Members {
			memberSet[m] = true
		}
		for i := range p.items {
			if p.items[i].Kind == pickerItemRepo && memberSet[p.items[i].Name] {
				p.items[i].Checked = item.Checked
			}
			// Also sync other sets that share members.
			if p.items[i].Kind == pickerItemSet && i != idx {
				p.syncSetState(&p.items[i])
			}
		}
	}

	// If toggling an individual repo, update any sets it belongs to.
	if item.Kind == pickerItemRepo {
		for i := range p.items {
			if p.items[i].Kind == pickerItemSet {
				p.syncSetState(&p.items[i])
			}
		}
	}
}

// syncSetState checks if all members of a set are selected and
// updates the set's checked state accordingly.
func (p *repoPicker) syncSetState(setItem *pickerItem) {
	allChecked := true
	for _, member := range setItem.Members {
		found := false
		for _, item := range p.items {
			if item.Kind == pickerItemRepo && item.Name == member {
				found = true
				if !item.Checked {
					allChecked = false
				}
				break
			}
		}
		if !found {
			allChecked = false
		}
	}
	setItem.Checked = allChecked
}

func (p *repoPicker) moveUp() {
	if p.cursor > 0 {
		p.cursor--
	}
}

func (p *repoPicker) moveDown() {
	if p.cursor < len(p.visibleIndices)-1 {
		p.cursor++
	}
}

// selectedRepos returns the deduplicated sorted list of selected repos.
func (p *repoPicker) selectedRepos() []string {
	seen := make(map[string]bool)
	var result []string

	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}

	for _, item := range p.items {
		if !item.Checked {
			continue
		}
		if item.Kind == pickerItemSet {
			for _, m := range item.Members {
				add(m)
			}
		} else {
			add(item.Name)
		}
	}

	sort.Strings(result)
	return result
}

// updateFilter processes a key message while the filter input is
// focused. Returns a Cmd if the textinput needs one (e.g. blink).
func (p *repoPicker) updateFilter(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(msg)
	p.refilter()
	return cmd
}

func renderPickerHint(theme Theme, key, label string) string {
	keyStyle := lipgloss.NewStyle().Foreground(theme.Accent)
	labelStyle := lipgloss.NewStyle().Foreground(theme.Muted)
	return keyStyle.Render(key) + " " + labelStyle.Render(label)
}

func (p *repoPicker) view() string {
	var b strings.Builder

	titleStyle := p.styles.ModalTitle
	b.WriteString(titleStyle.Render(p.title))
	b.WriteString("\n\n")

	if p.filtering {
		inputLabel := lipgloss.NewStyle().Bold(true).Foreground(p.theme.Title)
		b.WriteString(inputLabel.Render("Filter: "))
		b.WriteString(p.filter.View())
		b.WriteString("\n\n")
	} else if strings.TrimSpace(p.filter.Value()) != "" {
		filterStyle := lipgloss.NewStyle().Foreground(p.theme.Muted)
		b.WriteString(filterStyle.Render(
			fmt.Sprintf("filter: %s (/ to change, esc to clear)", p.filter.Value())))
		b.WriteString("\n\n")
	}

	if len(p.visibleIndices) == 0 {
		b.WriteString(p.styles.ModalContent.Render("  No matching repos."))
		b.WriteString("\n")
	}

	for vi, srcIdx := range p.visibleIndices {
		item := p.items[srcIdx]

		cursor := "  "
		if vi == p.cursor {
			cursor = "> "
		}

		checkbox := "[ ]"
		if item.Checked {
			checkbox = "[x]"
		}

		var label string
		if item.Kind == pickerItemSet {
			members := strings.Join(item.Members, ", ")
			label = fmt.Sprintf("%s (%s)", item.Name, members)
		} else {
			label = item.Name
		}

		line := fmt.Sprintf("%s%s %s", cursor, checkbox, label)

		if vi == p.cursor {
			style := lipgloss.NewStyle().
				Bold(true).
				Background(p.styles.SelectedRow.GetBackground())
			b.WriteString(style.Render(line))
		} else if item.Kind == pickerItemSet {
			b.WriteString(p.styles.ModalTitle.Render(line))
		} else {
			b.WriteString(p.styles.ModalContent.Render(line))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	hints := []string{
		renderPickerHint(p.theme, "j/k", "navigate"),
		renderPickerHint(p.theme, "space", "toggle"),
		renderPickerHint(p.theme, "/", "filter"),
		renderPickerHint(p.theme, "enter", "create"),
		renderPickerHint(p.theme, "esc", "cancel"),
	}
	b.WriteString(strings.Join(hints, "  "))

	return b.String()
}

// --- Tabbed repo picker wrapping local + remote ---

type pickerTab int

const (
	pickerTabLocal  pickerTab = iota
	pickerTabRemote
)

type remotePhase int

const (
	remotePhaseOrgSelect remotePhase = iota // pick from config orgs list
	remotePhaseOrgEntry                     // type a custom org name
	remotePhaseSearch                       // search repos within an org
)

type remoteState struct {
	phase       remotePhase
	orgInput    textinput.Model
	searchInput textinput.Model
	activeOrg   string
	configOrgs  []string
	results     []string
	cursor      int
	searching   bool
	searchGen   uint64
	cloning     bool
	err         error
	ghAvailable bool
}

type tabbedRepoPicker struct {
	activeTab    pickerTab
	local        repoPicker
	remote       remoteState
	styles       Styles
	theme        Theme
	title        string
	repoSets     *workspace.RepoSets
	excludeRepos map[string]bool // repos already in workspace (add-repos flow)
}

func newTabbedRepoPicker(title string, sets map[string][]string, allRepos []string, styles Styles, rs *workspace.RepoSets, ghAvailable bool) tabbedRepoPicker {
	local := newRepoPicker(title, sets, allRepos, styles)

	orgInput := textinput.New()
	orgInput.Placeholder = "org name..."
	orgInput.CharLimit = 60
	orgInput.Focus()

	searchInput := textinput.New()
	searchInput.Placeholder = "search repos..."
	searchInput.CharLimit = 60

	var configOrgs []string
	if rs != nil {
		configOrgs = rs.GitHubOrgs
	}

	phase := remotePhaseOrgEntry
	if len(configOrgs) > 0 {
		phase = remotePhaseOrgSelect
	} else {
		orgInput.Focus()
	}

	return tabbedRepoPicker{
		activeTab: pickerTabLocal,
		local:     local,
		remote: remoteState{
			phase:       phase,
			orgInput:    orgInput,
			searchInput: searchInput,
			activeOrg:   "",
			configOrgs:  configOrgs,
			ghAvailable: ghAvailable,
		},
		styles:   styles,
		theme:    styles.theme,
		title:    title,
		repoSets: rs,
	}
}

func (tp *tabbedRepoPicker) selectedRepos() []string {
	return tp.local.selectedRepos()
}

func (tp *tabbedRepoPicker) switchToLocal() {
	tp.activeTab = pickerTabLocal
	tp.remote.orgInput.Blur()
	tp.remote.searchInput.Blur()
	if tp.local.filtering {
		tp.local.filter.Focus()
	}
}

func (tp *tabbedRepoPicker) switchToRemote() {
	tp.activeTab = pickerTabRemote
	tp.local.filter.Blur()
	switch tp.remote.phase {
	case remotePhaseOrgSelect:
		// No text input to focus — cursor navigation only.
	case remotePhaseOrgEntry:
		tp.remote.orgInput.Focus()
	case remotePhaseSearch:
		tp.remote.searchInput.Focus()
	}
}

func (tp *tabbedRepoPicker) refreshLocalRepos() {
	if tp.repoSets == nil {
		return
	}
	allRepos, err := tp.repoSets.ListRepos()
	if err != nil {
		return
	}
	var available []string
	for _, r := range allRepos {
		if !tp.excludeRepos[r] {
			available = append(available, r)
		}
	}
	var sets map[string][]string
	if tp.repoSets != nil {
		sets = tp.repoSets.Sets
	}
	tp.local = newRepoPicker(tp.title, sets, available, tp.styles)
}

func (tp *tabbedRepoPicker) remoteSelectedRepo() string {
	if tp.remote.cursor < 0 || tp.remote.cursor >= len(tp.remote.results) {
		return ""
	}
	return tp.remote.results[tp.remote.cursor]
}

func (tp *tabbedRepoPicker) handleRemoteKey(msg tea.KeyMsg) tea.Cmd {
	r := &tp.remote

	if !r.ghAvailable {
		if msg.String() == "esc" {
			return nil // caller handles cancel
		}
		return nil
	}

	switch r.phase {
	case remotePhaseOrgSelect:
		return tp.handleOrgSelectKey(msg)
	case remotePhaseOrgEntry:
		return tp.handleOrgEntryKey(msg)
	case remotePhaseSearch:
		return tp.handleSearchKey(msg)
	}
	return nil
}

func (tp *tabbedRepoPicker) orgSelectOptions() []string {
	// Config orgs plus "Other..." for manual entry.
	return append(append([]string{}, tp.remote.configOrgs...), "Other...")
}

func (tp *tabbedRepoPicker) handleOrgSelectKey(msg tea.KeyMsg) tea.Cmd {
	r := &tp.remote
	options := tp.orgSelectOptions()

	switch msg.String() {
	case "j", "down":
		if r.cursor < len(options)-1 {
			r.cursor++
		}
	case "k", "up":
		if r.cursor > 0 {
			r.cursor--
		}
	case "enter":
		if r.cursor >= len(options) {
			return nil
		}
		selected := options[r.cursor]
		if selected == "Other..." {
			r.phase = remotePhaseOrgEntry
			r.cursor = 0
			r.orgInput.Reset()
			r.orgInput.Focus()
			return r.orgInput.Cursor.BlinkCmd()
		}
		r.activeOrg = selected
		r.phase = remotePhaseSearch
		r.cursor = 0
		r.searchInput.Reset()
		r.searchInput.Focus()
		r.results = nil
		r.err = nil
		return r.searchInput.Cursor.BlinkCmd()
	case "esc":
		return nil // caller handles cancel
	}
	return nil
}

func (tp *tabbedRepoPicker) handleOrgEntryKey(msg tea.KeyMsg) tea.Cmd {
	r := &tp.remote

	switch msg.String() {
	case "enter":
		org := strings.TrimSpace(r.orgInput.Value())
		if org == "" {
			return nil
		}
		r.activeOrg = org
		r.phase = remotePhaseSearch
		r.orgInput.Blur()
		r.searchInput.Focus()
		r.searchInput.Reset()
		r.results = nil
		r.cursor = 0
		r.err = nil
		return r.searchInput.Cursor.BlinkCmd()
	case "esc":
		// Go back to org select if config orgs exist.
		if len(r.configOrgs) > 0 {
			r.phase = remotePhaseOrgSelect
			r.cursor = 0
			r.orgInput.Blur()
			return nil
		}
		return nil // caller handles cancel
	default:
		var cmd tea.Cmd
		r.orgInput, cmd = r.orgInput.Update(msg)
		return cmd
	}
}

func (tp *tabbedRepoPicker) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
	r := &tp.remote

	switch msg.String() {
	case "j", "down":
		if r.cursor < len(r.results)-1 {
			r.cursor++
		}
		return nil
	case "k", "up":
		if r.cursor > 0 {
			r.cursor--
		}
		return nil
	case "enter":
		// Handled by model.go (triggers clone)
		return nil
	case "esc":
		// If search has text, clear it.
		if strings.TrimSpace(r.searchInput.Value()) != "" {
			r.searchInput.Reset()
			r.results = nil
			r.cursor = 0
			r.err = nil
			return nil
		}
		// Go back to org selection.
		r.searchInput.Blur()
		r.activeOrg = ""
		r.results = nil
		r.cursor = 0
		r.err = nil
		if len(r.configOrgs) > 0 {
			r.phase = remotePhaseOrgSelect
			return nil
		}
		r.phase = remotePhaseOrgEntry
		r.orgInput.Focus()
		return r.orgInput.Cursor.BlinkCmd()
		return nil // caller handles cancel
	default:
		var cmd tea.Cmd
		r.searchInput, cmd = r.searchInput.Update(msg)
		return cmd
	}
}

func (tp *tabbedRepoPicker) isConfigOrg(org string) bool {
	for _, o := range tp.remote.configOrgs {
		if o == org {
			return true
		}
	}
	return false
}

func (tp *tabbedRepoPicker) view() string {
	var b strings.Builder

	titleStyle := tp.styles.ModalTitle
	b.WriteString(titleStyle.Render(tp.title))
	b.WriteString("\n\n")

	b.WriteString(tp.renderTabBar())
	b.WriteString("\n\n")

	switch tp.activeTab {
	case pickerTabLocal:
		b.WriteString(tp.local.viewBody())
	case pickerTabRemote:
		b.WriteString(tp.renderRemoteBody())
	}

	b.WriteString("\n")
	b.WriteString(tp.renderHints())

	return b.String()
}

// viewEmbedded renders the picker without its own title or hints,
// for use when embedded inside another component (like the wizard).
func (tp *tabbedRepoPicker) viewEmbedded() string {
	var b strings.Builder

	b.WriteString(tp.renderTabBar())
	b.WriteString("\n\n")

	switch tp.activeTab {
	case pickerTabLocal:
		b.WriteString(tp.local.viewBody())
	case pickerTabRemote:
		b.WriteString(tp.renderRemoteBody())
	}

	return b.String()
}

func (tp *tabbedRepoPicker) renderTabBar() string {
	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(tp.theme.Accent).
		Background(tp.theme.Surface).
		Padding(0, 1)
	inactiveStyle := lipgloss.NewStyle().
		Foreground(tp.theme.Muted).
		Padding(0, 1)

	localLabel := " Local "
	remoteLabel := " Remote "
	if tp.activeTab == pickerTabLocal {
		localLabel = activeStyle.Render(localLabel)
		remoteLabel = inactiveStyle.Render(remoteLabel)
	} else {
		localLabel = inactiveStyle.Render(localLabel)
		remoteLabel = activeStyle.Render(remoteLabel)
	}

	return "  " + localLabel + " " + remoteLabel
}

// viewBody renders just the body of the local picker (filter + items)
// without the title, so the tabbed wrapper can provide its own title
// and tab bar.
func (p *repoPicker) viewBody() string {
	var b strings.Builder

	if p.filtering {
		inputLabel := lipgloss.NewStyle().Bold(true).Foreground(p.theme.Title)
		b.WriteString(inputLabel.Render("Filter: "))
		b.WriteString(p.filter.View())
		b.WriteString("\n\n")
	} else if strings.TrimSpace(p.filter.Value()) != "" {
		filterStyle := lipgloss.NewStyle().Foreground(p.theme.Muted)
		b.WriteString(filterStyle.Render(
			fmt.Sprintf("filter: %s (/ to change, esc to clear)", p.filter.Value())))
		b.WriteString("\n\n")
	}

	if len(p.visibleIndices) == 0 {
		b.WriteString(p.styles.ModalContent.Render("  No matching repos."))
		b.WriteString("\n")
	}

	for vi, srcIdx := range p.visibleIndices {
		item := p.items[srcIdx]

		cursor := "  "
		if vi == p.cursor {
			cursor = "> "
		}

		checkbox := "[ ]"
		if item.Checked {
			checkbox = "[x]"
		}

		var label string
		if item.Kind == pickerItemSet {
			members := strings.Join(item.Members, ", ")
			label = fmt.Sprintf("%s (%s)", item.Name, members)
		} else {
			label = item.Name
		}

		line := fmt.Sprintf("%s%s %s", cursor, checkbox, label)

		if vi == p.cursor {
			style := lipgloss.NewStyle().
				Bold(true).
				Background(p.styles.SelectedRow.GetBackground())
			b.WriteString(style.Render(line))
		} else if item.Kind == pickerItemSet {
			b.WriteString(p.styles.ModalTitle.Render(line))
		} else {
			b.WriteString(p.styles.ModalContent.Render(line))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func (tp *tabbedRepoPicker) renderRemoteBody() string {
	var b strings.Builder
	r := &tp.remote

	if !r.ghAvailable {
		b.WriteString(tp.styles.ModalContent.Render(
			"  GitHub CLI (gh) not found or not authenticated.\n" +
				"  Install gh and run 'gh auth login' to enable."))
		b.WriteString("\n")
		return b.String()
	}

	switch r.phase {
	case remotePhaseOrgSelect:
		headerStyle := lipgloss.NewStyle().Bold(true).Foreground(tp.theme.Title)
		b.WriteString(headerStyle.Render("Select organization:"))
		b.WriteString("\n\n")

		options := tp.orgSelectOptions()
		for i, opt := range options {
			cursor := "  "
			if i == r.cursor {
				cursor = "> "
			}
			line := cursor + opt
			if i == r.cursor {
				style := lipgloss.NewStyle().
					Bold(true).
					Background(tp.styles.SelectedRow.GetBackground())
				b.WriteString(style.Render(line))
			} else {
				b.WriteString(tp.styles.ModalContent.Render(line))
			}
			b.WriteString("\n")
		}

	case remotePhaseOrgEntry:
		inputLabel := lipgloss.NewStyle().Bold(true).Foreground(tp.theme.Title)
		b.WriteString(inputLabel.Render("Organization: "))
		b.WriteString(r.orgInput.View())
		b.WriteString("\n\n")

		hintStyle := lipgloss.NewStyle().Foreground(tp.theme.Muted).Italic(true)
		b.WriteString(hintStyle.Render("  Tip: add github_orgs to config.yaml to save orgs permanently"))
		b.WriteString("\n")

	case remotePhaseSearch:
		orgLabel := lipgloss.NewStyle().Bold(true).Foreground(tp.theme.Title)
		b.WriteString(orgLabel.Render(fmt.Sprintf("Org: %s", r.activeOrg)))
		b.WriteString("\n\n")

		inputLabel := lipgloss.NewStyle().Bold(true).Foreground(tp.theme.Title)
		b.WriteString(inputLabel.Render("Search: "))
		b.WriteString(r.searchInput.View())
		b.WriteString("\n\n")

		if r.err != nil {
			errStyle := lipgloss.NewStyle().Foreground(tp.theme.Error)
			b.WriteString(errStyle.Render(fmt.Sprintf("  Error: %v", r.err)))
			b.WriteString("\n")
		} else if r.searching {
			searchingStyle := lipgloss.NewStyle().Foreground(tp.theme.Muted)
			b.WriteString(searchingStyle.Render("  Searching..."))
			b.WriteString("\n")
		} else if len(r.results) == 0 && strings.TrimSpace(r.searchInput.Value()) != "" {
			b.WriteString(tp.styles.ModalContent.Render("  No repos found."))
			b.WriteString("\n")
		} else if len(r.results) == 0 {
			hintStyle := lipgloss.NewStyle().Foreground(tp.theme.Muted)
			b.WriteString(hintStyle.Render("  Type to search repos..."))
			b.WriteString("\n")
		} else {
			for i, repo := range r.results {
				cursor := "  "
				if i == r.cursor {
					cursor = "> "
				}
				line := cursor + repo

				if i == r.cursor {
					style := lipgloss.NewStyle().
						Bold(true).
						Background(tp.styles.SelectedRow.GetBackground())
					b.WriteString(style.Render(line))
				} else {
					b.WriteString(tp.styles.ModalContent.Render(line))
				}
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

func (tp *tabbedRepoPicker) renderHints() string {
	hints := []string{
		renderPickerHint(tp.theme, "tab", "switch tab"),
	}

	switch tp.activeTab {
	case pickerTabLocal:
		hints = append(hints,
			renderPickerHint(tp.theme, "j/k", "navigate"),
			renderPickerHint(tp.theme, "space", "toggle"),
			renderPickerHint(tp.theme, "/", "filter"),
			renderPickerHint(tp.theme, "enter", "create"),
			renderPickerHint(tp.theme, "esc", "cancel"),
		)
	case pickerTabRemote:
		r := &tp.remote
		if r.ghAvailable {
			switch r.phase {
			case remotePhaseOrgSelect:
				hints = append(hints,
					renderPickerHint(tp.theme, "j/k", "navigate"),
					renderPickerHint(tp.theme, "enter", "select"),
				)
			case remotePhaseSearch:
				if len(r.results) > 0 {
					hints = append(hints,
						renderPickerHint(tp.theme, "j/k", "navigate"),
						renderPickerHint(tp.theme, "enter", "clone"),
					)
				}
			}
		}
		hints = append(hints, renderPickerHint(tp.theme, "esc", "back"))
	}

	return strings.Join(hints, "  ")
}
