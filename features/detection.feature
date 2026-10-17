Feature: Detecting a pick face below its minimum
  The detector reads the event-fed copies of bin levels, the forward map and the
  layout. When the projected level of a pick face reaches the policy minimum it
  raises ONE request to move stock from the fullest reserve bin, never above the
  policy maximum.

  Background:
    Given site "SITE-1" has forward bin "F-01" for "SKU-1" and reserve bins "R-01,R-02"
    And a policy for "SKU-1" at site "SITE-1" with min 10 max 60 and multiple 1

  Scenario: A pick face below its minimum raises one request
    Given bin "F-01" of site "SITE-1" holds 8 on hand and 3 reserved of "SKU-1"
    When bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    Then there are 1 requests for "SKU-1" at site "SITE-1"
    And the request for "SKU-1" at site "SITE-1" moves 55 units from "R-01" to "F-01" with priority "NORMAL"
    And the request for "SKU-1" at site "SITE-1" is "Requested"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested"

  Scenario: A second evaluation does not duplicate the request
    Given bin "F-01" of site "SITE-1" holds 8 on hand and 3 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    When I evaluate site "SITE-1"
    Then the sweep raised 0, found 1 already open, 0 healthy, 0 without policy and 0 without source
    And there are 1 requests for "SKU-1" at site "SITE-1"
    When I evaluate site "SITE-1"
    Then the sweep raised 0, found 1 already open, 0 healthy, 0 without policy and 0 without source
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested"

  Scenario: The sweep raises what the consumers could not
    Given the approved slot plan of site "SITE-1" assigns "SKU-1" to "F-01", "SKU-2" to "F-02"
    And the forward location "F-02" is registered
    And bin "F-02" of site "SITE-1" holds 2 on hand and 0 reserved of "SKU-2"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-2"
    And there are 0 requests for "SKU-2" at site "SITE-1"
    And a policy for "SKU-2" at site "SITE-1" with min 10 max 60 and multiple 1
    When I evaluate site "SITE-1"
    Then the sweep raised 1, found 0 already open, 0 healthy, 0 without policy and 1 without source
    And the request for "SKU-2" at site "SITE-1" moves 58 units from "R-01" to "F-02" with priority "NORMAL"

  Scenario: A pick face with nothing available is URGENT
    Given bin "F-01" of site "SITE-1" holds 3 on hand and 3 reserved of "SKU-1"
    When bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    Then the request for "SKU-1" at site "SITE-1" moves 60 units from "R-01" to "F-01" with priority "URGENT"

  Scenario: Reserved stock is not available
    Given bin "F-01" of site "SITE-1" holds 20 on hand and 15 reserved of "SKU-1"
    When bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    Then the request for "SKU-1" at site "SITE-1" moves 55 units from "R-01" to "F-01" with priority "NORMAL"

  Scenario: Without a reserve bin holding stock the pick face is NO_SOURCE
    Given bin "F-01" of site "SITE-1" holds 8 on hand and 0 reserved of "SKU-1"
    When I evaluate site "SITE-1"
    Then the sweep raised 0, found 0 already open, 0 healthy, 0 without policy and 1 without source
    And there are 0 requests for "SKU-1" at site "SITE-1"
    And the pick face "F-01" of site "SITE-1" has status "NO_SOURCE"

  Scenario: Reserve stock that is all reserved is no source
    Given bin "F-01" of site "SITE-1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 20 on hand and 20 reserved of "SKU-1"
    When I evaluate site "SITE-1"
    Then the pick face "F-01" of site "SITE-1" has status "NO_SOURCE"

  Scenario: A pick face above its minimum is OK and raises nothing
    Given bin "F-01" of site "SITE-1" holds 40 on hand and 0 reserved of "SKU-1"
    When bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    Then there are 0 requests for "SKU-1" at site "SITE-1"
    And the pick face "F-01" of site "SITE-1" has status "OK"

  Scenario: The fullest reserve bin supplies the request
    Given bin "F-01" of site "SITE-1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 30 of "SKU-1"
    And bin "R-02" of site "SITE-1" holds 50 of "SKU-1"
    When bin "F-01" of site "SITE-1" holds 8 on hand and 0 reserved of "SKU-1"
    Then the request for "SKU-1" at site "SITE-1" moves 50 units from "R-02" to "F-01" with priority "NORMAL"

  Scenario: A small reserve bin gives a partial fill
    Given bin "F-01" of site "SITE-1" holds 8 on hand and 0 reserved of "SKU-1"
    When bin "R-01" of site "SITE-1" holds 20 of "SKU-1"
    Then the request for "SKU-1" at site "SITE-1" moves 20 units from "R-01" to "F-01" with priority "NORMAL"

  Scenario: The quantity is rounded down to a multiple and never overfills
    Given a policy for "SKU-2" at site "SITE-1" with min 10 max 60 and multiple 6
    And the approved slot plan of site "SITE-1" assigns "SKU-1" to "F-01", "SKU-2" to "F-02"
    And the forward location "F-02" is registered
    And bin "F-02" of site "SITE-1" holds 4 on hand and 0 reserved of "SKU-2"
    When bin "R-01" of site "SITE-1" holds 200 of "SKU-2"
    Then the request for "SKU-2" at site "SITE-1" moves 54 units from "R-01" to "F-02" with priority "NORMAL"

  Scenario: Less than one multiple of need raises nothing
    Given a policy for "SKU-2" at site "SITE-1" with min 58 max 60 and multiple 6
    And the approved slot plan of site "SITE-1" assigns "SKU-1" to "F-01", "SKU-2" to "F-02"
    And the forward location "F-02" is registered
    And bin "F-02" of site "SITE-1" holds 57 on hand and 0 reserved of "SKU-2"
    When bin "R-01" of site "SITE-1" holds 200 of "SKU-2"
    Then there are 0 requests for "SKU-2" at site "SITE-1"
    And the pick face "F-02" of site "SITE-1" has status "NO_SOURCE"

  Scenario: A stale bin level changes nothing
    Given bin "F-01" of site "SITE-1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    When bin "F-01" of site "SITE-1" reports a level of 0 on hand for "SKU-1" that is not newer
    Then the consumer outcome is "stale"
    And there are 0 requests for "SKU-1" at site "SITE-1"
    And the pick face "F-01" of site "SITE-1" has status "OK"

  Scenario: A redelivered level is processed once
    Given bin "F-01" of site "SITE-1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    When the last inventory event is delivered again
    Then the consumer outcome is "duplicate"
    And there are 1 requests for "SKU-1" at site "SITE-1"

  Scenario: A level that can never be applied is invalid
    When bin "F-01" of site "SITE-1" holds -4 on hand and 0 reserved of "SKU-1"
    Then the command fails with "invalid event"

  Scenario: A slot in a reserve zone is not a pick face
    Given the approved slot plan of site "SITE-1" assigns "SKU-1" to "F-01", "SKU-3" to "R-01"
    When I list the pick faces of site "SITE-1"
    Then the pick faces are "F-01"

  Scenario: A decommissioned forward location is not a pick face
    Given the location "F-01" is decommissioned
    When I list the pick faces of site "SITE-1"
    Then the pick faces are ""

  Scenario: Pick faces are listed with their status and can be filtered
    Given the approved slot plan of site "SITE-1" assigns "SKU-1" to "F-01", "SKU-2" to "F-02", "SKU-3" to "F-03"
    And the forward location "F-02" is registered
    And the forward location "F-03" is registered
    And a policy for "SKU-2" at site "SITE-1" with min 10 max 60 and multiple 1
    And bin "F-01" of site "SITE-1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "F-02" of site "SITE-1" holds 2 on hand and 0 reserved of "SKU-2"
    When I list the pick faces of site "SITE-1"
    Then the pick faces are "F-01,F-02,F-03"
    And the pick face "F-01" of site "SITE-1" has status "OK"
    And the pick face "F-02" of site "SITE-1" has status "NO_SOURCE"
    And the pick face "F-03" of site "SITE-1" has status "NO_POLICY"
    When I list the pick faces of site "SITE-1" with status "NO_POLICY"
    Then the pick faces are "F-03"
    When I list the pick faces of site "SITE-1" with a page size of 2
    Then the pick faces are "F-01,F-02"
    And there is a next page
    When I list the next page of pick faces
    Then the pick faces are "F-03"
    And there is no next page

  Scenario: A manual request sized by the detector rules
    Given bin "F-01" of site "SITE-1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    And I cancel the request for "SKU-1" at site "SITE-1"
    When I request a replenishment of "SKU-1" at site "SITE-1"
    Then the command succeeds
    And the request for "SKU-1" at site "SITE-1" moves 52 units from "R-01" to "F-01" with priority "NORMAL"

  Scenario: A manual request with a quantity
    Given bin "F-01" of site "SITE-1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    When I request a replenishment of 20 units of "SKU-1" at site "SITE-1"
    Then the command succeeds
    And the request for "SKU-1" at site "SITE-1" moves 20 units from "R-01" to "F-01" with priority "NORMAL"

  Scenario Outline: A manual request that cannot be raised
    Given bin "F-01" of site "SITE-1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 30 of "SKU-1"
    When I request a replenishment of <quantity> units of "<sku>" at site "SITE-1"
    Then the command fails with "<error>"

    Examples:
      | sku   | quantity | error                                       |
      | SKU-1 | 21       | exceeds the policy maximum                  |
      | SKU-1 | 0        | quantity must be at least 1                 |
      | SKU-9 | 5        | assigns no forward bin                      |

  Scenario: A manual request needs a policy
    Given the approved slot plan of site "SITE-1" assigns "SKU-1" to "F-01", "SKU-2" to "F-02"
    When I request a replenishment of 5 units of "SKU-2" at site "SITE-1"
    Then the command fails with "no replenishment policy"

  Scenario: A manual request needs reserve stock
    Given bin "F-01" of site "SITE-1" holds 40 on hand and 0 reserved of "SKU-1"
    When I request a replenishment of 5 units of "SKU-1" at site "SITE-1"
    Then the command fails with "no reserve bin"

  Scenario: A manual request without a quantity needs a pick face below its minimum
    Given bin "F-01" of site "SITE-1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    When I request a replenishment of "SKU-1" at site "SITE-1"
    Then the command fails with "nothing to replenish"

  Scenario: A manual request without a quantity needs a source
    Given bin "F-01" of site "SITE-1" holds 8 on hand and 0 reserved of "SKU-1"
    When I request a replenishment of "SKU-1" at site "SITE-1"
    Then the command fails with "no reserve bin"

  Scenario: Only one request is open per pick face
    Given bin "F-01" of site "SITE-1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    When I request a replenishment of 5 units of "SKU-1" at site "SITE-1"
    Then the command fails with "already open"
    And there are 1 requests for "SKU-1" at site "SITE-1"
