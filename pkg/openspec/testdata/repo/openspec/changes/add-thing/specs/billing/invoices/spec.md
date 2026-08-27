## Purpose

Invoice numbering.

## ADDED Requirements

### Requirement: Sequential invoice numbers
The system SHALL number invoices sequentially per billing account.

#### Scenario: Numbers increment
- **WHEN** a second invoice is issued for the same account
- **THEN** its number is one greater than the previous invoice's
