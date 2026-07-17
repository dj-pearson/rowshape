---
title: Agents & MCP
description: Put rowshape inside the agent's turn with the MCP server and init --agent.
sidebar:
  order: 1
---

An MCP server the agent never calls is decoration. Getting rowshape into the
agent's context is a separate job from exposing the tools, and it is the one that
closes the loop.

`rowshape mcp` serves four deliberately thin tools — `describe_shape`,
`validate_migration`, `explain_finding`, `plan_against` — from the same binary.
`rowshape init --agent` wires them in: MCP client config, a versioned agent rule,
and a pre-commit backstop.

The full guide lands here.
