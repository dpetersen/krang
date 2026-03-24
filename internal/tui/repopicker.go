package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	title  string
	items  []pickerItem
	cursor int
	styles Styles
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

	return repoPicker{
		title:  title,
		items:  items,
		styles: styles,
	}
}

func (p *repoPicker) toggle() {
	if p.cursor < 0 || p.cursor >= len(p.items) {
		return
	}

	item := &p.items[p.cursor]
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
			if p.items[i].Kind == pickerItemSet && i != p.cursor {
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
			// Member not in the standalone list (only in set).
			// Check if it's selected via the set itself.
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
	if p.cursor < len(p.items)-1 {
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

func (p *repoPicker) view() string {
	var b strings.Builder

	titleStyle := p.styles.ModalTitle
	b.WriteString(titleStyle.Render(p.title))
	b.WriteString("\n\n")

	for i, item := range p.items {
		cursor := "  "
		if i == p.cursor {
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

		if i == p.cursor {
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
	b.WriteString(p.styles.Header.Render("j/k: navigate  space: toggle  enter: create  esc: cancel"))

	return b.String()
}
