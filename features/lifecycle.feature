Feature: The life of a replenishment request
  A request is raised by detection, then ends in exactly one way: the
  inventory-storage relocation that executes it, a newer slot plan that drops
  its assignment, the removal of its policy, or the operator.

  Background:
    Given site "SITE-1" has forward bin "F-01" for "SKU-1" and reserve bins "R-01"
    And a policy for "SKU-1" at site "SITE-1" with min 10 max 60 and multiple 1
    And bin "F-01" of site "SITE-1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-1"

  Scenario: A matching relocation completes the request
    When inventory relocates 52 units of "SKU-1" from "R-01" to "F-01" at site "SITE-1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    Then the consumer outcome is "applied"
    And the request for "SKU-1" at site "SITE-1" is "Completed"
    And the request for "SKU-1" at site "SITE-1" was completed with 52 units moved
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCompleted"

  Scenario: A partial relocation records the units it moved
    When inventory relocates 30 units of "SKU-1" from "R-01" to "F-01" at site "SITE-1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    Then the request for "SKU-1" at site "SITE-1" was completed with 30 units moved

  Scenario: A completed request frees the pick face for the next detection
    Given inventory relocates 52 units of "SKU-1" from "R-01" to "F-01" at site "SITE-1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    And bin "F-01" of site "SITE-1" holds 4 on hand and 0 reserved of "SKU-1"
    Then there are 2 requests for "SKU-1" at site "SITE-1"
    And the request for "SKU-1" at site "SITE-1" is "Requested"

  Scenario: A relocation delivered twice completes the request once
    Given inventory relocates 52 units of "SKU-1" from "R-01" to "F-01" at site "SITE-1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    When the last inventory event is delivered again
    Then the consumer outcome is "duplicate"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCompleted"

  Scenario Outline: A relocation that is not ours is ignored
    When inventory relocates <quantity> units of "<sku>" from "<from>" to "<to>" at site "<site>" with reason "<reason>" referencing "<ref>"
    Then the consumer outcome is "ignored"
    And the request for "SKU-1" at site "SITE-1" is "Requested"

    Examples:
      | quantity | sku   | from | to   | site    | reason       | ref          |
      | 52       | SKU-1 | R-01 | F-01 | SITE-1  | SLOTTING     | repl-0001    |
      | 52       | SKU-1 | R-01 | F-01 | SITE-1  | MANUAL       | repl-0001    |
      | 52       | SKU-1 | R-01 | F-01 | SITE-1  | REPLENISHMENT | plan-0001   |
      | 52       | SKU-1 | R-01 | F-01 | SITE-1  | REPLENISHMENT | repl-9999   |
      | 52       | SKU-1 | R-01 | F-01 | SITE-2  | REPLENISHMENT | repl-0001   |
      | 52       | SKU-2 | R-01 | F-01 | SITE-1  | REPLENISHMENT | repl-0001   |
      | 52       | SKU-1 | R-02 | F-01 | SITE-1  | REPLENISHMENT | repl-0001   |
      | 52       | SKU-1 | R-01 | F-02 | SITE-1  | REPLENISHMENT | repl-0001   |

  Scenario: A relocation of our request that moved nothing is invalid
    When inventory relocates 0 units of "SKU-1" from "R-01" to "F-01" at site "SITE-1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    Then the command fails with "invalid event"
    And the request for "SKU-1" at site "SITE-1" is "Requested"

  Scenario: A new slot plan that moves the SKU cancels the request and raises it for the new pick face
    Given the forward location "F-02" is registered
    When the approved slot plan of site "SITE-1" assigns "SKU-1" to "F-02"
    Then the consumer outcome is "applied"
    And the oldest request for "SKU-1" at site "SITE-1" was cancelled because "ASSIGNMENT_CHANGED"
    And the request for "SKU-1" at site "SITE-1" is "Requested"
    And the open request for "SKU-1" at site "SITE-1" has forward bin "F-02"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCancelled,ReplenishmentRequested"

  Scenario: A new slot plan that drops the SKU cancels the request
    When the approved slot plan of site "SITE-1" assigns nothing
    Then the request for "SKU-1" at site "SITE-1" was cancelled because "ASSIGNMENT_CHANGED"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCancelled"
    And there are 0 pick faces at site "SITE-1"

  Scenario: A new slot plan that keeps the assignment leaves the request alone
    When the approved slot plan of site "SITE-1" assigns "SKU-1" to "F-01"
    Then the request for "SKU-1" at site "SITE-1" is "Requested"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested"

  Scenario: A slot plan of another site leaves this site alone
    When the approved slot plan of site "SITE-2" assigns nothing
    Then the request for "SKU-1" at site "SITE-1" is "Requested"
    And there are 1 pick faces at site "SITE-1"

  Scenario: A slot plan assigning a SKU twice is invalid
    When the approved slot plan of site "SITE-1" assigns "SKU-1" to "F-01", "SKU-1" to "F-02"
    Then the command fails with "invalid event"
    And the request for "SKU-1" at site "SITE-1" is "Requested"

  Scenario: The operator cancels an open request
    When I cancel the request for "SKU-1" at site "SITE-1"
    Then the command succeeds
    And the request for "SKU-1" at site "SITE-1" was cancelled because "OPERATOR"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCancelled"

  Scenario: A request that is no longer open cannot be cancelled
    Given I cancel the request for "SKU-1" at site "SITE-1"
    When I cancel the request for "SKU-1" at site "SITE-1"
    Then the command fails with "not open"

  Scenario: A cancelled request ignores a late relocation
    Given I cancel the request for "SKU-1" at site "SITE-1"
    When inventory relocates 52 units of "SKU-1" from "R-01" to "F-01" at site "SITE-1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    Then the consumer outcome is "ignored"
    And the request for "SKU-1" at site "SITE-1" is "Cancelled"

  Scenario: Requests are listed newest first and can be filtered by state
    Given I cancel the request for "SKU-1" at site "SITE-1"
    And I evaluate site "SITE-1"
    When I list the requests of site "SITE-1"
    Then the listed requests are "repl-0002,repl-0001"
    When I list the requests of site "SITE-1" in state "Cancelled"
    Then the listed requests are "repl-0001"
    When I list the requests of site "SITE-1" with a page size of 1
    Then the listed requests are "repl-0002"
    And there is a next page
    When I list the next page of requests
    Then the listed requests are "repl-0001"
    And there is no next page

  Scenario: A request can be read by id and an unknown id is not found
    When I get the request "repl-0001"
    Then the command succeeds
    When I get the request "repl-4242"
    Then the command fails with "not found"
    When I get the request "nope"
    Then the command fails with "request id must start with repl-"
