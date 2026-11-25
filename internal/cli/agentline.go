package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/sutrakam/graphlume/internal/config"
)

// agentLine is the first line an agent would read for the same question: the
// summary of the tool answer, with the word it opens with when the index cannot
// stand behind it.
//
// The relationship commands print their own tables, and a table does not carry
// the doubts the agent tool states: that a function passed as a value may have
// callers the graph cannot see, that a confidence floor left some out, that a
// type has subtypes no edge records. Two surfaces that say different things
// about one question are how a person acts on a number an agent was told to
// distrust, so the commands call the tool and print its line.
type agentLine struct {
	// Summary is the first line. Empty when the tool could not be asked.
	Summary string
	// Outcome is Response.Outcome: "ambiguous_name", "kind_not_indexed" or "".
	Outcome string
}

// askAgentTool calls tool in-process and returns its first line. Any failure
// returns the zero value, so a command falls back to what it printed before and
// never fails because the extra line could not be had.
func askAgentTool(ctx context.Context, g *Global, tool string, args map[string]any) agentLine {
	srv, closeServer, err := openAgentServer(ctx, g, agentServerOptions{Profile: config.MCPFull})
	if err != nil {
		return agentLine{}
	}
	defer closeServer()
	inv, err := srv.NewInvoker(ctx, "cli")
	if err != nil {
		return agentLine{}
	}
	defer func() { _ = inv.Close() }()

	payload, err := json.Marshal(args)
	if err != nil {
		return agentLine{}
	}
	res, err := inv.Call(ctx, tool, payload)
	if err != nil || res.IsError {
		return agentLine{}
	}
	var out struct {
		Summary string `json:"summary"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(res.Structured, &out); err != nil {
		return agentLine{}
	}
	return agentLine{Summary: out.Summary, Outcome: out.Outcome}
}

// print writes the line above a command's own output. An ambiguous name is not
// printed: the command answers for the first definition and says so below, and
// a line saying the tool answered for none of them would contradict the table.
func (l agentLine) print(w io.Writer) {
	if l.Summary == "" || l.Outcome == "ambiguous_name" {
		return
	}
	fmt.Fprintf(w, "%s\n\n", l.Summary)
}

// refused reports whether the tool declined to answer because the name has no
// definition of the kind the question is about.
func (l agentLine) refused() bool { return l.Outcome == "kind_not_indexed" }
