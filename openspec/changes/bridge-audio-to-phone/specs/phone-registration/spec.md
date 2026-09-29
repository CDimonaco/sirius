## ADDED Requirements

### Requirement: Sirius accepts an authenticated registration from a phone

Sirius SHALL answer SIP `REGISTER` requests and record where the registering phone can
be reached. A request whose credentials do not match the configured account SHALL be
challenged once and then refused, so that another device on the same network cannot
receive the user's meeting audio.

#### Scenario: A phone registers with valid credentials

- **WHEN** a `REGISTER` request arrives carrying a `Contact` header and credentials
  matching the configured account
- **THEN** Sirius answers `200 OK`, echoing the `Contact` and the granted `Expires`
- **AND** the contact address is reachable for subsequent calls

#### Scenario: A phone registers without credentials

- **WHEN** a `REGISTER` request arrives with no `Authorization` header
- **THEN** Sirius answers `401 Unauthorized` with a digest challenge
- **AND** no contact address is recorded

#### Scenario: A phone registers with wrong credentials

- **WHEN** a `REGISTER` request arrives whose digest response does not match the
  configured account
- **THEN** Sirius answers `403 Forbidden`
- **AND** no contact address is recorded

#### Scenario: A registration carries no contact

- **WHEN** an authenticated `REGISTER` request arrives with no `Contact` header
- **THEN** Sirius answers `400 Bad Request`
- **AND** any previously recorded contact for that account is left unchanged

### Requirement: A registration expires unless it is refreshed

Sirius SHALL grant an expiry between 60 and 3600 seconds, clamping the requested value
into that range and defaulting to 3600 seconds when the request states none. A contact
whose expiry has passed SHALL NOT be treated as reachable.

#### Scenario: The requested expiry is granted

- **WHEN** an authenticated `REGISTER` requests an expiry of 600 seconds
- **THEN** the response states `Expires: 600`

#### Scenario: A short expiry is clamped

- **WHEN** an authenticated `REGISTER` requests an expiry of 5 seconds
- **THEN** the response states `Expires: 60`

#### Scenario: A registration lapses

- **GIVEN** a phone registered with an expiry of 60 seconds
- **WHEN** 61 seconds pass with no further `REGISTER`
- **THEN** the phone is reported as not reachable

#### Scenario: A refresh extends the registration

- **GIVEN** a phone registered with an expiry of 60 seconds
- **WHEN** a second authenticated `REGISTER` arrives after 30 seconds
- **THEN** the phone stays reachable for 60 seconds from the second request

### Requirement: A phone can unregister

Sirius SHALL treat an authenticated `REGISTER` with `Expires: 0` as a request to
forget the contact.

#### Scenario: Explicit unregistration

- **GIVEN** a registered phone
- **WHEN** an authenticated `REGISTER` arrives with `Expires: 0`
- **THEN** Sirius answers `200 OK`
- **AND** the phone is reported as not reachable

### Requirement: A later registration replaces an earlier one

A phone that moves to a new address, which happens whenever DHCP reassigns it, SHALL
replace its own earlier contact rather than adding a second one.

#### Scenario: The same account registers from a new address

- **GIVEN** a phone registered from one address
- **WHEN** an authenticated `REGISTER` for the same account arrives from a different
  address
- **THEN** only the newer contact address is reachable
