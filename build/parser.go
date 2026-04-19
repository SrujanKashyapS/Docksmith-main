package build

import (
	"encoding/json"
	"fmt"
	"strings"
)

// InstructionType represents a Docksmithfile instruction keyword.
type InstructionType string

const (
	InstrFROM    InstructionType = "FROM"
	InstrCOPY    InstructionType = "COPY"
	InstrRUN     InstructionType = "RUN"
	InstrWORKDIR InstructionType = "WORKDIR"
	InstrENV     InstructionType = "ENV"
	InstrCMD     InstructionType = "CMD"
)

// Instruction is a single parsed Docksmithfile instruction.
type Instruction struct {
	Type InstructionType
	Args []string // parsed arguments
	Raw  string   // full original line (for cache key)
}

// ParseDocksmithfile parses a Docksmithfile and returns the instructions.
func ParseDocksmithfile(content string) ([]Instruction, error) {
	var instructions []Instruction
	lines := strings.Split(content, "\n")
	for lineNum, line := range lines {
		// Strip comments and trim whitespace.
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Split into keyword and rest.
		parts := strings.SplitN(line, " ", 2)
		keyword := strings.ToUpper(strings.TrimSpace(parts[0]))
		rest := ""
		if len(parts) > 1 {
			rest = strings.TrimSpace(parts[1])
		}

		instr := Instruction{Raw: line}

		switch InstructionType(keyword) {
		case InstrFROM:
			if rest == "" {
				return nil, fmt.Errorf("line %d: FROM requires an argument", lineNum+1)
			}
			instr.Type = InstrFROM
			instr.Args = []string{rest}

		case InstrCOPY:

			// COPY <src> <dest>
			copyArgs := strings.Fields(rest)
			if len(copyArgs) < 2 {
				return nil, fmt.Errorf("line %d: COPY requires <src> and <dest>", lineNum+1)
			}
			instr.Type = InstrCOPY
			instr.Args = copyArgs // may have multiple sources + one dest

		case InstrRUN:
			if rest == "" {
				return nil, fmt.Errorf("line %d: RUN requires a command", lineNum+1)
			}
			instr.Type = InstrRUN
			instr.Args = []string{rest}

		case InstrWORKDIR:
			if rest == "" {
				return nil, fmt.Errorf("line %d: WORKDIR requires a path", lineNum+1)
			}
			instr.Type = InstrWORKDIR
			instr.Args = []string{rest}

		case InstrENV:
			// ENV key=value
			if rest == "" {
				return nil, fmt.Errorf("line %d: ENV requires key=value", lineNum+1)
			}
			instr.Type = InstrENV
			instr.Args = []string{rest}

		case InstrCMD:
			// CMD ["exec","arg"]
			if rest == "" {
				return nil, fmt.Errorf("line %d: CMD requires a JSON array", lineNum+1)
			}
			var cmdArgs []string
			if err := json.Unmarshal([]byte(rest), &cmdArgs); err != nil {
				return nil, fmt.Errorf("line %d: CMD must be a JSON array: %w", lineNum+1, err)
			}
			instr.Type = InstrCMD
			instr.Args = cmdArgs
			instr.Raw = line

		default:
			return nil, fmt.Errorf("line %d: unknown instruction %q", lineNum+1, keyword)
		}

		instructions = append(instructions, instr)
	}

	if len(instructions) == 0 {
		return nil, fmt.Errorf("Docksmithfile is empty")
	}
	if instructions[0].Type != InstrFROM {
		return nil, fmt.Errorf("Docksmithfile must start with FROM")
	}

	return instructions, nil
}
