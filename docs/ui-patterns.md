# TUI Design Patterns and Research

Reference document for krang's UI design decisions, compiled from research
on successful TUIs (lazygit, k9s, btop, gitui), Bubble Tea patterns, and
modal interface design principles.

## Core Principles

### Responsiveness is Non-Negotiable

"A truly native TUI responds instantly to input." In Bubble Tea terms,
`Update()` and `View()` must be fast — offload expensive work to `tea.Cmd`
goroutines. Never block the event loop.

### Visual Hierarchy Through Color and Focus

- **Color coding** differentiates element types (active, warning, error)
- **Highlighting** clearly indicates the focused/selected element at all
  times
- **Borders and boxes** create visual regions (lazygit's "views" pattern)
- **Spinners and progress bars** for ongoing operations — never leave the
  user wondering "is it working?"
- **Semantic color, not decorative color**: if you removed color, layout
  and symbols should still convey hierarchy. Design in 16-color mode
  first; true color enhances but should never be the sole signal.
- **Dim unimportant data**: Use dim/faint styling for metadata
  (timestamps, IDs) and bold/bright for primary data (names, statuses).

### Keyboard-First, Mouse-Optional

- Arrow keys and vim-style hjkl for navigation
- **ESC is universal "go back"** — users should always know how to retreat
- Tab for cycling between panels/sections
- Enter for confirmation/action
- Single-letter shortcuts for frequent actions (lazygit: `c` commit,
  `f` fetch, `p` pull)

### Error Handling

Display errors inline (typically at the bottom, in red/error color),
auto-cleared on next interaction. Never crash — handle terminal resize,
unexpected states, and edge cases gracefully.

## Discoverability Patterns

### Progressive Disclosure (Layered Help)

- **Level 1**: Show only the most common actions in the footer (3-5 keys)
- **Level 2**: `?` opens full keybinding reference
- **Level 3**: Command palette for everything, searchable
- **Level 4**: Config file for power-user customization

Each level serves a different expertise stage without overwhelming
beginners.

### The Which-Key Pattern

Neovim's which-key.nvim is the gold standard: after pressing a leader key,
a popup appears showing all available next keys and their actions. This
eliminates memorization while teaching through use.

### Contextual Footer / Status Bar Hints

The most common pattern across successful TUIs:

- **lazygit**: Footer dynamically shows relevant keybindings for the
  current panel
- **btop**: Keyboard hints displayed inline, togglable regions via number
  keys
- **k9s**: Context-sensitive shortcuts shown at bottom
- **OpenCode**: Footer alternates between "Get started" hints and status
  info for new users

### Empty States as Onboarding

When a list is empty, use that space to teach: show what actions are
available, what the space would look like with content, or quickstart
instructions. Two parts instruction, one part personality.

## What Makes the Best TUIs Great

### lazygit — The Design Benchmark

- **Simultaneous visibility**: All relevant context visible at once in
  persistent panels
- **Consistency**: Same navigation model in every panel (j/k to move,
  enter to select, space to toggle)
- **Action keys mirror their names**: `c` = commit, `a` = add, `f` =
  fetch. Zero memorization.
- **Interactive guidance**: Push warns about divergence; rebase prompts
  clarify. "Teaches you on the way."
- **Contextual footer**: Shows only the keybindings relevant to the
  currently focused panel

### k9s — Real-Time Data Display

- **Resource browser pattern**: Navigate hierarchies of related resources
- **XRay view**: Drill-down into relationships
- **Toggleable sections**: Show/hide UI regions on demand
- **Real-time updates**: Data refreshes without user action, with visual
  indicators of changes

### btop/bottom — Adaptive Layout

- **Numbered regions (1-5, d)**: Each toggleable by pressing its ID
  character
- **Responsive resize**: Layout adapts meaningfully to terminal size, not
  just truncation
- **Graphed data**: Dense information conveyed visually (sparklines, bar
  charts)

### gitui — Speed as a Feature

- Blazing fast rendering even on large repos
- Minimal chrome, maximum content
- Clear modal states (staging, committing, branching each have distinct
  visual modes)

## Keyboard Shortcut Conventions

### Universal Conventions

| Key | Action | Notes |
|-----|--------|-------|
| `q` / `Esc` | Quit / back / close | `q` quits app or sub-view; `Esc` closes overlays |
| `?` | Help | Context-sensitive keybinding list |
| `/` | Search / filter | Nearly universal across TUIs |
| `j` / `k` | Down / up | Vim convention, expected by power users |
| Arrow keys | Navigation | For non-vim users; support both |
| `Enter` | Select / confirm | Primary action on focused item |
| `Tab` | Next panel / field | Shift+Tab for reverse |
| `g` / `G` | Go to top / bottom | Vim convention |
| `d` | Delete | With confirmation |
| `Space` | Toggle / select | For checkbox-like selection |
| `Ctrl+C` | Force quit | Should always work as escape hatch |

### Design Rules

- **Never require modifier keys for common actions**: Single characters
  (`d`, `p`, `f`) are faster and more discoverable than `Ctrl+D`.
- **Reserve Ctrl+ combos for dangerous or rare actions**: `Ctrl+K` for
  kill-all is fine because it's harder to hit accidentally.
- **Consistent "back" behavior**: `Esc` and `q` should always move you
  one level up. Never trap users in a sub-view.
- **Capital letters for variants**: `D` for force-delete vs `d` for
  soft-delete. `P` for push-force vs `p` for push.

## Modal Interface Principles

### The Scalable UI Paradigm

Core insight from EmacsConf 2020 (Sid Kasivajhula): "Learn a simple
language once, apply it to vastly different aspects of your interface."
The same keybindings do different things in different contexts, in
**sensible and predictable** ways.

- Dedicated modes for specific nouns (tasks, windows, settings) create
  smaller modal spaces where keys can be more targeted
- The same conceptual action (delete, move, open, close) uses the same
  key across modes but operates on different objects
- This is often easier than learning unique bindings for every action

### Reducing Context Switching

Seek efficiency by reducing keystrokes and cognitive load. Consistent
keybinding patterns across modes/panels mean less mental context switching
even when the visual context changes.

### When to Use Modes

- **Prefer modeless for primary workflows**: If the main loop is "look at
  list, act on item," keep it modeless.
- **Use modes for input**: Text input (search, rename) necessarily enters
  an "input mode." Make it obvious with a visible text cursor and changed
  footer keybindings.
- **Use modal overlays for confirmations and forms**: Temporary, focused
  interruptions. Capture all input and dismiss cleanly with Esc.
- **Keep modes shallow**: One level deep is fine (normal -> input). Two
  levels is risky. Three is a usability disaster.
- **Always indicate the current mode**: Visual indicator in status bar.

## Handling Specific UX Challenges

### Loading States

- **Spinner component** from bubbles for indeterminate waits
- **Progress bar** when you know the total work
- **Status text** that updates as stages complete ("Cloning repo...",
  "Installing hooks...")
- **Disable navigation** to in-progress items to prevent interruption
- Never leave the user staring at a static screen during async work

### Confirmation Dialogs

- **State the consequence, not the action**: "This will stop the Claude
  process and delete the workspace" is better than "Are you sure?"
- **Default to the safe option** (N for destructive actions)
- **Use verb labels, not Yes/No**: "Complete task" / "Cancel" is better
  than "Yes" / "No"
- **Color-code the destructive option**: Red/bold for destructive, dim
  for cancel. Never rely on color alone.
- **Escape always cancels**
- **Tiered severity**:
  - Low risk (park): No confirmation needed
  - Medium risk (freeze, complete): Simple y/n confirmation
  - High risk (delete data): Require typing a word to confirm
- **Undo over confirmation when possible**: If reversible, skip the
  dialog and offer undo via a brief toast: "Task parked. Press z to
  undo."

### Toast Notifications / Transient Feedback

- Brief messages that appear and auto-dismiss ("Task frozen", "Workspace
  created")
- Distinct from persistent status (which stays until state changes)
- Use a timer-based message in Bubble Tea to clear after 2-3 seconds

### Information Density

- **Minimum viable terminal**: Design for 80x24 first. Use
  percentage-based layouts with min/max constraints.
- **Truncation strategy**: Show the most important data first
  (left-aligned). Truncate with ellipsis from the right. For paths,
  truncate from the left (`.../deep/path/file.go`).
- **Relative timestamps**: "2m ago" is more scannable than full
  timestamps.
- **Dense but not cluttered**: The difference is alignment and
  whitespace. Consistent column alignment in tables and 1-character
  gutters between panels make dense UIs readable.
- **Progressive detail**: List view shows summary, pressing Enter or
  focusing shows detail. Don't try to show everything at once.

## Bubble Tea / Charm Specific Patterns

### Architecture: Model Tree

- Organize as a hierarchy: root model routes messages, child models
  handle their domains
- Root handles three routing paths:
  1. **Global keys** (quit, help) processed directly
  2. **Current/focused model** receives interaction messages
  3. **All children** receive system messages (WindowSizeMsg, etc.)

### Layout: Avoid Brittle Arithmetic

Don't hardcode heights. Instead of `Height(m.height - 1 - 1)`, use:

```go
Height(m.height - lipgloss.Height(header) - lipgloss.Height(footer))
```

Self-documenting and adapts to styling changes.

### Overlays and Modals

Use the bubbletea-overlay pattern: wrap two `tea.Model`s (background +
foreground), composite the foreground onto the background in `View()`.
This cleanly separates modal logic from the main UI.

### Concurrency

- Commands (`tea.Cmd`) run concurrently; messages arrive in
  unpredictable order
- User input remains ordered (single goroutine)
- Use `tea.Sequence()` when order matters
- **Never modify state outside the event loop** — race conditions where
  `View()` executes before changes complete

### State Machine Pattern for Multi-Step Operations

For long-running workflows (task creation, workspace setup):

```go
type Stage struct {
    Name           string
    Action         func() error
    IsComplete     bool
    IsCompleteFunc func() bool  // skip already-done work
    Reset          func() error // cleanup on failure
}
```

Execute stages asynchronously, send completion messages back to the event
loop. Stop the pipeline on any error.

### Testing

Use `teatest` for end-to-end testing: send key inputs, wait for output
strings, verify state transitions. Golden files support regression
testing of full renders.

## Actionable Patterns Summary

| Pattern | Priority | Description |
|---------|----------|-------------|
| Contextual footer hints | High | Show 3-5 relevant keybindings for current focus |
| `?` help overlay | High | Full keybinding reference, categorized |
| Spinner/progress for async | High | Never leave user wondering if something is working |
| Consequence-aware confirms | High | State what will happen, not just "are you sure?" |
| Toast notifications | Medium | Transient confirmation of completed actions |
| Empty state guidance | Medium | First-run and empty-list states teach usage |
| Consistent vim-style nav | Done | hjkl, enter, esc across all views |
| Adaptive layout on resize | Medium | Use lipgloss.Height() not hardcoded arithmetic |
| Command palette | Low | Fuzzy-searchable action finder for discoverability |
| Which-key popup | Low | Show available keys after prefix/delay |

## Sources

- [How TUIs Make Dev Tools Feel Native](https://notes.suhaib.in/docs/tech/utilities/making-dev-tools-feel-native-with-tui-interfaces/)
- [Tips for Building Bubble Tea Programs](https://leg100.github.io/en/posts/building-bubbletea-programs/)
- [The Bubbletea State Machine Pattern](https://zackproser.com/blog/bubbletea-state-machine)
- [The (lazy) Git UI You Didn't Know You Need](https://www.bwplotka.dev/2025/lazygit/)
- [lazygit GitHub](https://github.com/jesseduffield/lazygit)
- [k9s - Manage Your Kubernetes Clusters In Style](https://k9scli.io/)
- [btop GitHub](https://github.com/aristocratos/btop)
- [bottom GitHub](https://github.com/ClementTsang/bottom)
- [gitui GitHub](https://github.com/gitui-org/gitui)
- [which-key.nvim](https://github.com/folke/which-key.nvim)
- [Beyond Vim and Emacs: A Scalable UI Paradigm (EmacsConf 2020)](https://emacsconf.org/2020/talks/07/)
- [Bubble Tea GitHub](https://github.com/charmbracelet/bubbletea)
- [Bubbles - TUI Components](https://github.com/charmbracelet/bubbles)
- [bubbletea-overlay package](https://pkg.go.dev/github.com/quickphosphat/bubbletea-overlay)
- [OpenCode TUI Docs](https://opencode.ai/docs/tui/)
- [awesome-tuis](https://github.com/rothgar/awesome-tuis)
- [Modes in User Interfaces: When They Help and When They Hurt (NN/g)](https://www.nngroup.com/articles/modes/)
- [Modal & Nonmodal Dialogs (NN/g)](https://www.nngroup.com/articles/modal-nonmodal-dialog/)
- [Confirmation Dialogs Can Prevent User Errors (NN/g)](https://www.nngroup.com/articles/confirmation-dialog/)
- [How to Design Better Destructive Action Modals](https://uxpsychology.substack.com/p/how-to-design-better-destructive)
- [How to Manage Dangerous Actions in UIs (Smashing Magazine)](https://www.smashingmagazine.com/2024/09/how-manage-dangerous-actions-user-interfaces/)
