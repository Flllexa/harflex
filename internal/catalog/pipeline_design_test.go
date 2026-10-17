package catalog

import (
	"testing"

	"github.com/persioflexa/harflex/internal/sdd"
)

func TestPlanSourceDigestIncludesBrainstorm(t *testing.T) {
	document := func(stage sdd.Stage, content string) PipelineDesignDocument {
		return PipelineDesignDocument{Stage: stage, Content: content, ContentDigest: PipelineDesignContentDigest(content)}
	}
	documents := map[sdd.Stage]PipelineDesignDocument{sdd.Discovery: document(sdd.Discovery, "D"), sdd.Spec: document(sdd.Spec, "S")}
	withoutBrainstorm := PipelineDesignSourceDigest(sdd.Plan, documents)
	if got := PipelineDesignSourceDigest(sdd.Brainstorm, documents); got != withoutBrainstorm || got == "" {
		t.Fatalf("Brainstorm must depend on Discovery and SPEC exactly as Plan did: %q vs %q", got, withoutBrainstorm)
	}
	documents[sdd.Brainstorm] = document(sdd.Brainstorm, `{"flow":"flowchart TD"}`)
	withBrainstorm := PipelineDesignSourceDigest(sdd.Plan, documents)
	if withBrainstorm == withoutBrainstorm {
		t.Fatal("Plan's source digest ignores the Brainstorm it was written from")
	}
	documents[sdd.Brainstorm] = document(sdd.Brainstorm, `{"flow":"flowchart LR"}`)
	if PipelineDesignSourceDigest(sdd.Plan, documents) == withBrainstorm {
		t.Fatal("a different Brainstorm must give Plan a different source digest")
	}
	if PipelineDesignSourceDigest(sdd.Brainstorm, documents) != withoutBrainstorm {
		t.Fatal("Brainstorm's own content must not enter its source digest")
	}
}

func TestBrainstormDocumentHasItsOwnSizeLimit(t *testing.T) {
	if MaxPipelineDesignDocumentBytesFor(sdd.Brainstorm) != 512*1024 || MaxPipelineDesignDocumentBytesFor(sdd.Spec) != MaxPipelineDesignDocumentBytes {
		t.Fatalf("limits = %d / %d", MaxPipelineDesignDocumentBytesFor(sdd.Brainstorm), MaxPipelineDesignDocumentBytesFor(sdd.Spec))
	}
}
