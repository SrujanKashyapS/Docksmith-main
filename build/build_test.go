package build_test

import (
	"testing"

	"github.com/SrujanKashyapS/Docksmith/build"
)

func TestParseDocksmithfile_Basic(t *testing.T) {
	content := `FROM busybox:latest
WORKDIR /app
ENV APP=hello
COPY hello.sh /app/hello.sh
RUN chmod +x /app/hello.sh
CMD ["/bin/sh", "/app/hello.sh"]`

	instructions, err := build.ParseDocksmithfile(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 6 {
		t.Fatalf("expected 6 instructions, got %d", len(instructions))
	}

	tests := []struct {
		idx  int
		typ  build.InstructionType
		args []string
	}{
		{0, build.InstrFROM, []string{"busybox:latest"}},
		{1, build.InstrWORKDIR, []string{"/app"}},
		{2, build.InstrENV, []string{"APP=hello"}},
		{3, build.InstrCOPY, []string{"hello.sh", "/app/hello.sh"}},
		{4, build.InstrRUN, []string{"chmod +x /app/hello.sh"}},
		{5, build.InstrCMD, []string{"/bin/sh", "/app/hello.sh"}},
	}
	for _, tt := range tests {
		instr := instructions[tt.idx]
		if instr.Type != tt.typ {
			t.Errorf("instruction %d: expected type %s, got %s", tt.idx, tt.typ, instr.Type)
		}
		if len(instr.Args) != len(tt.args) {
			t.Errorf("instruction %d: expected %d args, got %d", tt.idx, len(tt.args), len(instr.Args))
			continue
		}
		for i, a := range tt.args {
			if instr.Args[i] != a {
				t.Errorf("instruction %d arg %d: expected %q, got %q", tt.idx, i, a, instr.Args[i])
			}
		}
	}
}

func TestParseDocksmithfile_CommentsAndBlankLines(t *testing.T) {
	content := `# This is a comment
FROM scratch

# Another comment
WORKDIR /tmp
CMD ["/bin/sh"]`

	instructions, err := build.ParseDocksmithfile(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 3 {
		t.Fatalf("expected 3 instructions, got %d", len(instructions))
	}
	if instructions[0].Type != build.InstrFROM {
		t.Errorf("first instruction should be FROM, got %s", instructions[0].Type)
	}
}

func TestParseDocksmithfile_MissingFROM(t *testing.T) {
	content := `WORKDIR /app
CMD ["/bin/sh"]`
	_, err := build.ParseDocksmithfile(content)
	if err == nil {
		t.Fatal("expected error for missing FROM, got nil")
	}
}

func TestParseDocksmithfile_InvalidCMD(t *testing.T) {
	content := `FROM scratch
CMD not-a-json-array`
	_, err := build.ParseDocksmithfile(content)
	if err == nil {
		t.Fatal("expected error for invalid CMD JSON, got nil")
	}
}

func TestParseDocksmithfile_UnknownInstruction(t *testing.T) {
	content := `FROM scratch
EXPOSE 8080`
	_, err := build.ParseDocksmithfile(content)
	if err == nil {
		t.Fatal("expected error for unknown instruction, got nil")
	}
}

func TestParseDocksmithfile_Empty(t *testing.T) {
	_, err := build.ParseDocksmithfile("")
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func TestParseDocksmithfile_COPY_MultipleArgs(t *testing.T) {
	content := `FROM scratch
COPY a.txt b.txt /dest/`
	instructions, err := build.ParseDocksmithfile(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	copyInstr := instructions[1]
	if copyInstr.Type != build.InstrCOPY {
		t.Fatalf("expected COPY, got %s", copyInstr.Type)
	}
	if len(copyInstr.Args) != 3 {
		t.Errorf("expected 3 args for COPY, got %d", len(copyInstr.Args))
	}
}

func TestParseDocksmithfile_Scratch(t *testing.T) {
	content := `FROM scratch
CMD ["/bin/sh"]`
	instructions, err := build.ParseDocksmithfile(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instructions[0].Args[0] != "scratch" {
		t.Errorf("expected 'scratch', got %q", instructions[0].Args[0])
	}
}

func TestCacheKey_Determinism(t *testing.T) {
	envs := []string{"Z=last", "A=first", "M=middle"}
	key1 := build.CacheKey("prevDigest", "RUN echo hello", "/app", envs, "")
	key2 := build.CacheKey("prevDigest", "RUN echo hello", "/app", envs, "")
	if key1 != key2 {
		t.Errorf("cache key not deterministic: %q != %q", key1, key2)
	}
}

func TestCacheKey_EnvOrderIndependent(t *testing.T) {
	envs1 := []string{"A=1", "B=2", "C=3"}
	envs2 := []string{"C=3", "A=1", "B=2"}
	key1 := build.CacheKey("prev", "RUN echo", "/", envs1, "")
	key2 := build.CacheKey("prev", "RUN echo", "/", envs2, "")
	if key1 != key2 {
		t.Errorf("cache key should be order-independent for envs: %q != %q", key1, key2)
	}
}

func TestCacheKey_DiffersOnChange(t *testing.T) {
	base := build.CacheKey("prev", "RUN echo", "/", nil, "")

	if k := build.CacheKey("other-prev", "RUN echo", "/", nil, ""); k == base {
		t.Error("expected different key when prev digest changes")
	}
	if k := build.CacheKey("prev", "RUN echo hello", "/", nil, ""); k == base {
		t.Error("expected different key when instruction changes")
	}
	if k := build.CacheKey("prev", "RUN echo", "/app", nil, ""); k == base {
		t.Error("expected different key when workdir changes")
	}
	if k := build.CacheKey("prev", "RUN echo", "/", []string{"X=1"}, ""); k == base {
		t.Error("expected different key when env changes")
	}
	if k := build.CacheKey("prev", "RUN echo", "/", nil, "abc123"); k == base {
		t.Error("expected different key when srcFileHash changes")
	}
}
