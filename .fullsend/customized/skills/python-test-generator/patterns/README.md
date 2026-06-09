# End-to-End Pattern Detection

This directory contains pattern detection configuration for python Python/pytest test generation.

## Files

- **pattern_rules.yaml** - Pattern detection rules for template selection (add your own)

## Pattern Detection Flow

1. Read `patterns/pattern_rules.yaml` (template selection logic)
2. Match scenario against patterns
3. Select appropriate template
4. Generate code using selected template

## Adding Patterns

Create a `pattern_rules.yaml` with rules extracted from your project's test repository.
The file maps scenario characteristics (keywords, component names) to template selections.

Project-specific patterns can also be placed in `config/projects/{name}/patterns/`.
