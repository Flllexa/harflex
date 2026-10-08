package security

type Profile string

const (
	Ask              Profile = "ask"
	TrustedWorkspace Profile = "trusted_workspace"
	// FullAccess lets the agent write, run commands and call network tools without asking. The file tools stay
	// confined to the root by their PathGuard, but a shell command or a local MCP process runs with the person's
	// own privileges and is not sandboxed. It is a choice the person confirms per project.
	FullAccess Profile = "full_access"
	Sandbox    Profile = "sandbox"
)

type Risk string

const (
	ReadOnly    Risk = "read_only"
	Write       Risk = "write"
	Shell       Risk = "shell"
	Network     Risk = "network"
	Destructive Risk = "destructive"
)

type Decision string

const (
	Allow   Decision = "allow"
	AskUser Decision = "ask_user"
	Deny    Decision = "deny"
)

type Request struct {
	Profile      Profile
	Risk         Risk
	WithinRoot   bool
	SandboxReady bool
}

func Decide(request Request) Decision {
	if !request.WithinRoot {
		return Deny
	}

	if request.Profile == Sandbox && !request.SandboxReady {
		return Deny
	}

	if request.Risk == Destructive {
		switch request.Profile {
		case Ask, TrustedWorkspace, Sandbox:
			return AskUser
		case FullAccess:
			return Allow
		default:
			return Deny
		}
	}

	switch request.Profile {
	case Ask:
		switch request.Risk {
		case ReadOnly:
			return Allow
		case Write, Shell, Network:
			return AskUser
		default:
			return Deny
		}
	case TrustedWorkspace:
		switch request.Risk {
		case ReadOnly, Write:
			return Allow
		case Shell, Network:
			return AskUser
		default:
			return Deny
		}
	case FullAccess:
		switch request.Risk {
		case ReadOnly, Write, Shell, Network:
			return Allow
		default:
			return Deny
		}
	case Sandbox:
		switch request.Risk {
		case ReadOnly, Write, Shell, Network:
			return Allow
		default:
			return Deny
		}
	default:
		return Deny
	}
}
