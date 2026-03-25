package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
