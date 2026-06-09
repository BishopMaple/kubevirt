# End-to-End Python/pytest Test Reference Examples

This directory holds **real working python test examples** from your project's test repository.

## Purpose

These examples serve as:
1. **Pattern library** - Real code patterns the generator should follow
2. **Quality benchmark** - Generated code should match these patterns
3. **Validation reference** - Compare generated tests against these examples
4. **Documentation** - Show what "good" python tests look like

## Usage

The `python-test-generator` skill references these examples to:
- Validate its pattern detection logic
- Ensure generated code follows your team's standards
- Learn new patterns as more examples are added
- Verify LSP analysis patterns are correctly applied

## Adding Examples

Place complete, working `.py` test files here from your project's test repo.

For each file added, document:
- Source location (repo, PR number)
- Pattern description
- Key patterns to learn

Also update `../patterns/pattern_rules.yaml` if new patterns are detected.

## Quality Criteria

Examples in this directory should be:
- Complete working tests (pass `pytest --collect-only`)
- From your project's test repository
- Following your team's coding standards
- Demonstrating python test patterns
- Include proper docstrings
