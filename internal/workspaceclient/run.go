package workspaceclient

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dpetersen/krang/internal/hooks"
)

// Request is one CLI invocation, after flag parsing and before any
// defaulting. Runner owns the defaulting so the rule ("no --task and no
// --cwd means the process's own cwd") is tested rather than trusted to
// each cobra binding.
type Request struct {
	Op     hooks.WorkspaceOp
	Params Params
	// JSON prints the response envelope exactly as krang sent it and
	// suppresses the human rendering. The exit code is the same either
	// way.
	JSON bool
}

// Runner executes one Request end to end: find the instance, call it,
// write output, produce an exit code. Everything it touches from the
// outside world is a field, so a test can supply all of it.
type Runner struct {
	Getenv  func(string) string
	Getwd   func() (string, error)
	Timeout time.Duration
	Stdout  io.Writer
	Stderr  io.Writer
}

func (r Runner) getenv(key string) string {
	if r.Getenv == nil {
		return os.Getenv(key)
	}
	return r.Getenv(key)
}

func (r Runner) getwd() (string, error) {
	if r.Getwd == nil {
		return os.Getwd()
	}
	return r.Getwd()
}

func (r Runner) stdout() io.Writer {
	if r.Stdout == nil {
		return os.Stdout
	}
	return r.Stdout
}

func (r Runner) stderr() io.Writer {
	if r.Stderr == nil {
		return os.Stderr
	}
	return r.Stderr
}

// Run performs the request and returns the process exit code. It writes
// every diagnostic itself, so a caller only has to pass the code on.
func (r Runner) Run(ctx context.Context, req Request) int {
	if err := r.applyDefaults(&req); err != nil {
		fmt.Fprintln(r.stderr(), err)
		return ExitError
	}
	if err := validate(req); err != nil {
		fmt.Fprintln(r.stderr(), err)
		return ExitError
	}

	client, err := NewFromEnv(r.getenv, r.Timeout)
	if err != nil {
		fmt.Fprintln(r.stderr(), err)
		return ExitCodeForError(err)
	}

	result, err := client.Call(ctx, req.Op, req.Params)
	if err != nil {
		fmt.Fprintln(r.stderr(), err)
		return ExitCodeForError(err)
	}

	if req.JSON {
		r.writeRaw(result.Raw)
	}

	code := ExitCodeFor(result.Envelope)
	if code != ExitOK {
		r.reportFailure(result, code)
		return code
	}
	if !req.JSON {
		r.render(req, result.Envelope)
	}
	return ExitOK
}

// applyDefaults fills in the cwd an agent didn't have to think about.
// `krang workspace list` with no arguments has to work from inside a
// task, because that is the call an agent makes first and the only one
// it can make without already knowing something.
func (r *Runner) applyDefaults(req *Request) error {
	if req.Params.Task != "" {
		// An explicit task wins on the server too. Sending the cwd
		// alongside it would be noise at best and a confusing "which one
		// did it use?" at worst.
		req.Params.Cwd = ""
		return nil
	}
	if req.Params.Cwd != "" {
		return nil
	}
	cwd, err := r.getwd()
	if err != nil {
		return fmt.Errorf("cannot determine the current directory, so krang has no way to tell "+
			"which task this is: %w\nPass --task <name> or --cwd <dir>", err)
	}
	req.Params.Cwd = cwd
	return nil
}

// validate catches the argument mistakes locally. The server checks the
// same things, but a round trip to be told "repo is required" is a round
// trip an agent waits on for nothing.
func validate(req Request) error {
	switch req.Op {
	case hooks.WorkspaceOpAdd:
		if req.Params.Repo == "" {
			return fmt.Errorf("--repo is required: name the repo to add, as listed by " +
				`"krang workspace repos"`)
		}
	case hooks.WorkspaceOpRemoveSlot:
		if req.Params.Dir == "" && req.Params.Repo == "" {
			return fmt.Errorf("name the working copy to remove: --dir <dir> as reported by " +
				`"krang workspace list", or --repo <repo> with an optional --label <label>`)
		}
	}
	return nil
}

func (r Runner) writeRaw(raw []byte) {
	out := r.stdout()
	_, _ = out.Write(raw)
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		fmt.Fprintln(out)
	}
}

// reportFailure writes the actionable half of a refusal to stderr. It
// runs for --json too: the envelope on stdout stays machine-clean, and
// the warning a caller most needs to see — "this might have applied" —
// is not something to make them derive.
func (r Runner) reportFailure(result Result, code int) {
	err := r.stderr()
	resp := result.Envelope

	message := resp.Message
	if message == "" {
		message = fmt.Sprintf("krang refused the request (HTTP %d)", result.HTTPStatus)
	}
	fmt.Fprintf(err, "krang: %s\n", message)
	fmt.Fprintf(err, "  reason: %s  applied: %s  exit: %d\n", orDash(resp.Reason), orDash(resp.Applied), code)

	for _, blocker := range resp.Blockers {
		fmt.Fprintf(err, "  blocker: %s %s: %s\n", blocker.Dir, blocker.Kind, blocker.Detail)
	}

	switch code {
	case ExitUnknown:
		fmt.Fprintln(err, "  DO NOT blindly retry: krang may have applied this request anyway.")
		fmt.Fprintln(err, `  Run "krang workspace list" and decide from what is actually there.`)
	case ExitRefused:
		fmt.Fprintln(err, "  Nothing was applied. Fix what the message names, then send the identical command again.")
	case ExitUnavailable:
		fmt.Fprintln(err, "  Nothing was applied. krang never took the request, so retrying later is safe.")
	}
}

// render writes the human form: one concise line, or one line per row
// for the two listings.
func (r Runner) render(req Request, resp hooks.WorkspaceResponse) {
	out := r.stdout()
	switch req.Op {
	case hooks.WorkspaceOpList:
		renderSlots(out, resp)
	case hooks.WorkspaceOpRepos:
		renderRepos(out, resp)
	case hooks.WorkspaceOpAdd:
		// Just the path. An agent's next move after adding a working copy
		// is to cd into it or read a file out of it, and making it parse a
		// sentence to find out where "it" is would be a small cruelty.
		fmt.Fprintln(out, addedPath(resp))
	case hooks.WorkspaceOpRemoveSlot:
		fmt.Fprintln(out, removedLine(resp))
	}
}

func renderSlots(out io.Writer, resp hooks.WorkspaceResponse) {
	if len(resp.Slots) == 0 {
		fmt.Fprintf(out, "task %s holds no working copies (workspace %s)\n",
			orDash(resp.Task), orDash(resp.Data["workspace_dir"]))
		return
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DIR\tREPO\tSLOT\tVCS\tBASE\tSTATE")
	for _, slot := range resp.Slots {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			slot.Dir, slot.Repo, orDash(slot.Slot), orDash(slot.VCS), orDash(slot.Base), slotState(slot))
	}
	_ = w.Flush()
}

// slotState collapses the two booleans into the word a caller acts on.
// "missing" is a recorded slot whose directory is gone — an
// inconsistency, not an absence. "unrecorded" is a directory krang did
// not create and can only guess about.
func slotState(slot hooks.SlotInfo) string {
	switch {
	case !slot.Exists:
		return "missing"
	case !slot.Recorded:
		return "unrecorded"
	default:
		return "ok"
	}
}

func renderRepos(out io.Writer, resp hooks.WorkspaceResponse) {
	if len(resp.Repos) == 0 {
		fmt.Fprintf(out, "no repos available (repos dir %s)\n", orDash(resp.Data["repos_dir"]))
		return
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REPO\tIN-TASK\tSETS")
	for _, repo := range resp.Repos {
		inTask := "no"
		if repo.InTask {
			inTask = "yes"
		}
		sets := append([]string(nil), repo.Sets...)
		sort.Strings(sets)
		fmt.Fprintf(w, "%s\t%s\t%s\n", repo.Name, inTask, orDash(strings.Join(sets, ",")))
	}
	_ = w.Flush()
}

// addedPath is the absolute path of the working copy that was created.
// The server computes it; the fallback only covers a krang older than
// the field, and says so by being obviously derived.
func addedPath(resp hooks.WorkspaceResponse) string {
	if path := resp.Data["path"]; path != "" {
		return path
	}
	if dir := resp.Data["workspace_dir"]; dir != "" && resp.Slot != nil {
		return strings.TrimSuffix(dir, "/") + "/" + resp.Slot.Dir
	}
	if resp.Slot != nil {
		return resp.Slot.Dir
	}
	return ""
}

func removedLine(resp hooks.WorkspaceResponse) string {
	dir, repo := "?", "?"
	if resp.Slot != nil {
		dir, repo = resp.Slot.Dir, resp.Slot.Repo
	}
	line := fmt.Sprintf("removed %s (repo %s) from task %s", dir, repo, orDash(resp.Task))
	if resp.Data["repo_dropped"] == "true" {
		line += fmt.Sprintf("; %s no longer holds any working copy of %s", orDash(resp.Task), repo)
	}
	if forgetErr := resp.Data["forget_error"]; forgetErr != "" {
		line += fmt.Sprintf("; the VCS identity was force-forgotten despite: %s", forgetErr)
	}
	return line
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
