## Purpose

Second-factor sign-in.

## ADDED Requirements

### Requirement: TOTP enrollment
The system SHALL let a signed-in user enroll a TOTP authenticator.

#### Scenario: Enrollment succeeds
- **WHEN** a signed-in user submits a valid TOTP code for a pending secret
- **THEN** the authenticator is activated

#### Scenario: Wrong code rejected
- **WHEN** the submitted code does not match the pending secret
- **THEN** enrollment fails and the secret stays pending

### Requirement: Recovery codes
The system SHALL issue single-use recovery codes on enrollment.

#### Scenario: Codes issued
- **WHEN** enrollment completes
- **THEN** ten single-use recovery codes are returned once

## MODIFIED Requirements

### Requirement: Sign-in
The system SHALL require a second factor when the account has an active authenticator.

#### Scenario: Second factor demanded
- **WHEN** a user with an active authenticator submits a correct password
- **THEN** the system asks for a TOTP code before issuing a session

## REMOVED Requirements

### Requirement: SMS fallback
**Reason**: SMS delivery is not trustworthy enough for a second factor.
