Feature: Consumer rules of the event-fed local copies (documentation-derived)
  Every consumer claims the CloudEvents id and applies its effect in one unit of
  work, rejects a payload that can never be applied (it is dead-lettered) and
  keeps levels and maps moving forward only (use cases 11 to 16, ADR 0005).

  Background:
    Given site "WH1" has forward bin "WH1-FWD-A01" for "SKU-1" and reserve bins "WH1-RSV-A07"
    And a policy for "SKU-1" at site "WH1" with min 10 max 60 and multiple 1
    And bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"

  # ---- bin levels ----------------------------------------------------------

  Scenario Outline: A bin level that can never be applied is invalid
    When a level of <on_hand> on hand and <reserved> reserved at version <version> for "<sku>" in bin "<bin>" of site "<site>" is delivered
    Then the command fails with "invalid event"
    And the pick face "WH1-FWD-A01" of site "WH1" shows 8 on hand, 0 reserved and 8 available

    Examples:
      | on_hand | reserved | version | sku   | bin         | site |
      | 5       | -1       | 2       | SKU-1 | WH1-FWD-A01 | WH1  |
      | 5       | 0        | 0       | SKU-1 | WH1-FWD-A01 | WH1  |
      | 5       | 0        | -2      | SKU-1 | WH1-FWD-A01 | WH1  |
      | 5       | 0        | 2       |       | WH1-FWD-A01 | WH1  |
      | 5       | 0        | 2       | SKU-1 |             | WH1  |
      | 5       | 0        | 2       | SKU-1 | WH1-FWD-A01 |      |

  Scenario Outline: A level replaces the stored one only when its version is greater
    Given a level of 40 on hand and 0 reserved at version 3 for "SKU-1" in bin "WH1-FWD-A01" of site "WH1" is delivered
    When a level of 5 on hand and 0 reserved at version <version> for "SKU-1" in bin "WH1-FWD-A01" of site "WH1" is delivered
    Then the consumer outcome is "<outcome>"
    And the pick face "WH1-FWD-A01" of site "WH1" shows <on_hand> on hand, 0 reserved and <on_hand> available

    Examples:
      | version | outcome | on_hand |
      | 2       | stale   | 40      |
      | 3       | stale   | 40      |
      | 4       | applied | 5       |

  # ---- slot plans ----------------------------------------------------------

  Scenario Outline: A slot plan that can never be applied is invalid and changes nothing
    When a slot plan "<plan>" of site "<site>" approved at "<approved_at>" assigning "<sku>" to "<slot>" is delivered
    Then the command fails with "invalid event"
    And the request for "SKU-1" at site "WH1" is "Requested"
    And there are 1 pick faces at site "WH1"

    Examples:
      | plan   | site | approved_at          | sku   | slot        |
      |        | WH1  | 2026-10-18T09:00:00Z | SKU-1 | WH1-FWD-A01 |
      | plan-9 |      | 2026-10-18T09:00:00Z | SKU-1 | WH1-FWD-A01 |
      | plan-9 | WH1  |                      | SKU-1 | WH1-FWD-A01 |
      | plan-9 | WH1  | 2026-10-18T09:00:00Z |       | WH1-FWD-A01 |
      | plan-9 | WH1  | 2026-10-18T09:00:00Z | SKU-1 |             |

  Scenario: A slot plan delivered twice is applied once
    Given the forward location "WH1-FWD-B01" is registered
    And the slot plan "plan-b" of site "WH1" approved at "2026-10-18T09:00:00Z" assigns "SKU-1" to "WH1-FWD-B01"
    When the last event is delivered again
    Then the consumer outcome is "duplicate"
    And there are 2 requests for "SKU-1" at site "WH1"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCancelled,ReplenishmentRequested"

  Scenario: A stale slot plan is still claimed, so its redelivery is a duplicate
    Given the slot plan "plan-old" of site "WH1" approved at "2026-10-17T08:00:00Z" assigns "SKU-1" to "WH1-FWD-A01"
    When the last event is delivered again
    Then the consumer outcome is "duplicate"
    And the request for "SKU-1" at site "WH1" is "Requested"

  # ---- layout --------------------------------------------------------------

  Scenario Outline: A zone registration missing a required field is invalid
    When a zone registration with id "<id>", site "<site>" and code "<code>" is delivered
    Then the command fails with "invalid event"

    Examples:
      | id    | site | code |
      |       | WH1  | BULK |
      | z-new |      | BULK |
      | z-new | WH1  |      |

  Scenario Outline: A location registration missing a required field is invalid
    When a location registration with code "<code>" and zone "<zone>" is delivered
    Then the command fails with "invalid event"

    Examples:
      | code        | zone  |
      |             | z-fwd |
      | WH1-FWD-B01 |       |

  Scenario: A decommission without a location code is invalid
    When a decommission of location "" is delivered
    Then the command fails with "invalid event"
    And there are 1 pick faces at site "WH1"
