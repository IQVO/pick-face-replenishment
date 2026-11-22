Feature: What raises a replenishment request (documentation-derived)
  Documented rules the earlier waves left unpinned: what re-evaluates a pick
  face and what must not (a reserve level for a face that had no source, a
  level kept for a SKU the map does not assign yet, the read-only pick-face
  view and the sweep of a site without a forward map), the boundary of the
  URGENT priority, a minimum of zero, the claim that names the consumer, the
  service clock on every fact and the blank-identity guard of a declaration.

  Background:
    Given site "WH1" has forward bin "WH1-FWD-A01" for "SKU-1" and reserve bins "WH1-RSV-A07"
    And a policy for "SKU-1" at site "WH1" with min 10 max 60 and multiple 1

  # ---- what a bin level re-evaluates --------------------------------------

  Scenario: Reserve stock arriving for a face that had no source raises the request
    Given bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    When I evaluate site "WH1"
    Then the sweep raised 0, found 0 already open, 0 healthy, 0 without policy and 1 without source
    And there are 0 requests for "SKU-1" at site "WH1"
    When bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    Then the consumer outcome is "applied"
    And the request for "SKU-1" at site "WH1" moves 52 units from "WH1-RSV-A07" to "WH1-FWD-A01" with priority "NORMAL"

  Scenario: A level for a SKU the forward map does not assign is stored and raises nothing
    Given bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    When bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-9"
    Then the consumer outcome is "applied"
    And there are 0 requests for "SKU-9" at site "WH1"
    And there are 0 requests for "SKU-1" at site "WH1"
    When I evaluate site "WH1"
    Then the sweep raised 0, found 0 already open, 0 healthy, 0 without policy and 1 without source

  Scenario: A level kept for a SKU the map does not assign yet is used when a plan assigns the SKU
    Given bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-7"
    And a policy for "SKU-7" at site "WH1" with min 10 max 60 and multiple 1
    And there are 0 requests for "SKU-7" at site "WH1"
    And the forward location "WH1-FWD-B01" is registered
    When the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-7" to "WH1-FWD-B01"
    Then the request for "SKU-7" at site "WH1" moves 60 units from "WH1-RSV-A07" to "WH1-FWD-B01" with priority "URGENT"

  Scenario: A sweep of a site without a forward map evaluates nothing
    When I evaluate site "WH9"
    Then the sweep evaluated 0 pick faces
    And the sweep raised 0, found 0 already open, 0 healthy, 0 without policy and 0 without source

  Scenario: Listing the pick faces runs the detector read-only and raises nothing
    Given bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    And I cancel the request for "SKU-1" at site "WH1"
    When I list the pick faces of site "WH1"
    Then the pick face "WH1-FWD-A01" of site "WH1" has status "BELOW_MIN"
    And there are 1 requests for "SKU-1" at site "WH1"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCancelled"
    When I evaluate site "WH1"
    Then the sweep raised 1, found 0 already open, 0 healthy, 0 without policy and 0 without source

  # ---- the boundary of the priority and of a minimum of zero --------------

  Scenario Outline: A request is URGENT exactly when nothing is available in the pick face
    Given bin "WH1-FWD-A01" of site "WH1" holds <on_hand> on hand and <reserved> reserved of "SKU-1"
    When bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    Then the request for "SKU-1" at site "WH1" moves <quantity> units from "WH1-RSV-A07" to "WH1-FWD-A01" with priority "<priority>"

    Examples:
      | on_hand | reserved | quantity | priority |
      | 0       | 0        | 60       | URGENT   |
      | 1       | 0        | 59       | NORMAL   |
      | 0       | 5        | 65       | URGENT   |
      | 6       | 5        | 59       | NORMAL   |

  Scenario Outline: A policy with a minimum of zero raises only an empty face
    Given a policy for "SKU-5" at site "WH1" with min 0 max 10 and multiple 1
    And the forward location "WH1-FWD-B01" is registered
    And bin "WH1-FWD-B01" of site "WH1" holds <on_hand> on hand and 0 reserved of "SKU-5"
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-5" to "WH1-FWD-B01"
    Then the pick face "WH1-FWD-B01" of site "WH1" has status "<before>"
    When bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-5"
    Then there are <requests> requests for "SKU-5" at site "WH1"
    And the pick face "WH1-FWD-B01" of site "WH1" has status "<after>"

    Examples:
      | on_hand | before    | requests | after |
      | 0       | NO_SOURCE | 1        | OK    |
      | 1       | OK        | 0        | OK    |

  # ---- the claim names the consumer ---------------------------------------

  Scenario: The same CloudEvents id is applied by two different consumers
    When a bin level of 40 on hand and 0 reserved at version 1 for "SKU-1" in bin "WH1-FWD-A01" of site "WH1" is delivered with the event id "evt-shared"
    Then the consumer outcome is "applied"
    When a zone registration with id "z-bulk", site "WH1" and code "BULK" is delivered with the event id "evt-shared"
    Then the consumer outcome is "applied"
    When the last event is delivered again
    Then the consumer outcome is "duplicate"

  Scenario: A stale bin level is still claimed, so its redelivery is a duplicate
    Given bin "WH1-FWD-A01" of site "WH1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    When a level of 5 on hand and 0 reserved at version 1 for "SKU-1" in bin "WH1-FWD-A01" of site "WH1" is delivered
    Then the consumer outcome is "stale"
    When the last event is delivered again
    Then the consumer outcome is "duplicate"
    And the pick face "WH1-FWD-A01" of site "WH1" shows 40 on hand, 0 reserved and 40 available

  # ---- every fact carries the service clock -------------------------------

  Scenario: Every request fact is stamped with the service clock
    Given bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    And the forward location "WH1-FWD-B01" is registered
    And a policy for "SKU-2" at site "WH1" with min 10 max 60 and multiple 1
    And bin "WH1-FWD-B01" of site "WH1" holds 4 on hand and 0 reserved of "SKU-2"
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-2" to "WH1-FWD-B01"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-2"
    Then the last ReplenishmentRequested fact is stamped "2026-10-17T09:00:00Z"
    When inventory relocates 52 units of "SKU-1" from "WH1-RSV-A07" to "WH1-FWD-A01" at site "WH1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    Then the last ReplenishmentCompleted fact is stamped "2026-10-17T09:00:00Z"
    When I cancel the request for "SKU-2" at site "WH1"
    Then the last ReplenishmentCancelled fact is stamped "2026-10-17T09:00:00Z"

  # ---- the identity guard of a declaration --------------------------------

  Scenario Outline: A blank site or SKU is refused before anything is stored
    When I declare a policy for "<sku>" at site "<site>" with min 10 max 60 and multiple 1
    Then the command fails with "must not be blank"
    And there are 1 policy declarations

    Examples:
      | sku   | site |
      |       | WH1  |
      | SKU-1 |      |
