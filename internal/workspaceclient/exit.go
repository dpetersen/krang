package workspaceclient

import (
	"errors"

	"github.com/dpetersen/krang/internal/hooks"
)

// Exit codes. An agent driving `krang workspace` decides what to do next
// from the code alone, so the set is small and the distinctions are the
// ones that change the next action — not a code per reason.
const (
	// ExitOK — the operation succeeded.
	ExitOK = 0

	// ExitError — a failure retrying cannot fix: bad arguments, no
	// KRANG_STATEFILE, a task or repo or slot that doesn't exist, an
	// operation that failed outright. Nothing was applied unless the
	// message says otherwise.
	ExitError = 1

	// ExitRefused — krang refused because the request conflicts with the
	// current state of the world, not with itself. Nothing was applied.
	// Fix what the message names (push the work, pass a label, remove a
	// slot to make room) and send the *identical* command again.
	ExitRefused = 2

	// ExitUnknown — krang may or may not have applied the request: it
	// accepted the work and then didn't answer in time, or it failed
	// partway through. DO NOT blindly retry; re-read the workspace and
	// decide from what is actually there.
	ExitUnknown = 3

	// ExitUnavailable — krang never took the request: the TUI isn't
	// listening, or it was too busy to accept before the deadline.
	// Nothing was applied, so retrying once krang is up is safe.
	ExitUnavailable = 4
)

// refusalReasons are the "conflicts with the state of the world"
// failures — the ones where the same command works once the caller has
// dealt with what the message names. They are exactly the server's 409s
// plus the 400s that are really "you need to tell me one more thing".
var refusalReasons = map[string]bool{
	hooks.ReasonUnsavedWork:     true,
	hooks.ReasonSharedWorkspace: true,
	hooks.ReasonSlotMissing:     true,
	hooks.ReasonLabelRequired:   true,
	hooks.ReasonSlotLimit:       true,
	hooks.ReasonAmbiguousSlot:   true,
}

// unavailableReasons are krang's own availability failures. The request
// provably never ran.
var unavailableReasons = map[string]bool{
	hooks.ReasonUnavailable: true,
	hooks.ReasonNotAccepted: true,
	hooks.ReasonExpired:     true,
}

// ExitCodeFor maps an envelope onto an exit code.
//
// It branches on applied *before* reason, because "might have happened"
// dominates every other classification: a caller that retries a request
// which may already have taken effect is the one failure mode this CLI
// exists to prevent.
func ExitCodeFor(resp hooks.WorkspaceResponse) int {
	if resp.Status == hooks.WorkspaceStatusOK {
		return ExitOK
	}
	if resp.Applied == hooks.AppliedUnknown {
		return ExitUnknown
	}
	switch {
	case refusalReasons[resp.Reason]:
		return ExitRefused
	case unavailableReasons[resp.Reason]:
		return ExitUnavailable
	default:
		return ExitError
	}
}

// ExitCodeForError maps a failure that never produced an envelope.
func ExitCodeForError(err error) int {
	var transport *TransportError
	if errors.As(err, &transport) {
		// The socket refused us, so no handler ever saw the request.
		return ExitUnavailable
	}
	var unsupported *UnsupportedAPIError
	if errors.As(err, &unsupported) {
		// Routing rejected it before any handler ran, so nothing was
		// applied — but relaunching krang is the only fix, which makes it
		// an error rather than something to retry.
		return ExitError
	}
	var protocol *ProtocolError
	if errors.As(err, &protocol) {
		// Something answered, but not krang's API. Whether it did
		// anything is exactly what we can't tell.
		return ExitUnknown
	}
	return ExitError
}

// ExitCodeHelp is the table every `krang workspace` subcommand prints in
// its --help. Repeating it per subcommand is deliberate: an agent reads
// one --help, not the tree.
const ExitCodeHelp = `Exit codes:
  0  Success.
  1  Error you cannot fix by retrying: bad arguments, KRANG_STATEFILE unset,
     unknown task/repo/slot, no workspace, operation_failed, or a krang too
     old to serve the endpoint. Nothing applied.
  2  Refused, but fixable: the request conflicts with the current state
     (unsaved_work, label_required, slot_limit, shared_workspace,
     slot_missing, ambiguous_slot). Nothing applied. Fix what the message
     names, then send the identical command again.
  3  UNKNOWN whether it applied (applied:"unknown"): krang accepted the work
     and did not answer in time, or failed partway. DO NOT blindly retry —
     run "krang workspace list" and decide from what is actually there.
  4  krang did not take the request: the TUI is not running, or was too busy
     to accept it before the deadline. Nothing applied; retrying is safe.`
