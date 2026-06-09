# Functional Go/Ginkgo Test Reference Examples

This directory holds **real working go test examples** from your project's source repository.

## Purpose

These examples serve as:
1. **Pattern library** - Real code patterns the generator should follow
2. **Quality benchmark** - Generated code should match these patterns
3. **Validation reference** - Compare generated tests against these examples
4. **Documentation** - Show what "good" go tests look like

## Usage

The `go-test-generator` skill references these examples to:
- Validate its pattern detection logic
- Ensure generated code follows your project's standards
- Learn new patterns as more examples are added

## Adding Examples

Place complete, working `_test.go` files here from your project's source repo.

For each file added, document:
- Source location (repo, directory)
- Pattern description (DescribeTable, Ordered contexts, etc.)
- Key patterns to learn

Also update `../patterns/pattern_rules.yaml` if new patterns are detected.

## Quality Criteria

Examples in this directory should be:
- Complete working tests (compile with your build system)
- From your project's source repository
- Following your project's coding standards
- Demonstrating go test patterns
- Well-documented with comments
