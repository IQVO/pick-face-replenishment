Feature: Life of a replenishment request (documentation-derived)
  The request is planned at version 1 and ends exactly once, at version 2, as
  Completed or Cancelled. Both are final. Only a relocation that is ours
  completes it; a newer slot plan, the removal of the policy or the operator
  cancel it (aggregate design canvas, use cases 6, 7, 12 and 13).

  Background:
    Given site "WH1" has forward bin "WH1-FWD-A01" for "SKU-1" and reserve bins "WH1-RSV-A07"
    And a policy for "SKU-1" at site "WH1" with min 10 max 60 and multiple 1
    And bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"

  # ---- the facts the transitions raise -----------------------------------

  Scenario: The request fact carries the source, the quantity, the priority and the policy it was sized with
    Then the request for "SKU-1" at site "WH1" is at version 1
    And the last ReplenishmentRequested fact moves 52 units from "WH1-RSV-A07" to "WH1-FWD-A01" with priority "NORMAL" sized by policy min 10 max 60 at version 1

  Scenario: The completion fact carries the units actually moved and the next version
    When inventory relocates 30 units of "SKU-1" from "WH1-RSV-A07" to "WH1-FWD-A01" at site "WH1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    Then the consumer outcome is "applied"
    And the request for "SKU-1" at site "WH1" is at version 2
    And the last ReplenishmentCompleted fact reports 30 units moved from "WH1-RSV-A07" to "WH1-FWD-A01" at version 2

  Scenario: A cancellation is announced with its reason and takes the request to version 2
    When I cancel the request for "SKU-1" at site "WH1"
    Then the request for "SKU-1" at site "WH1" is at version 2
    And the last ReplenishmentCancelled fact has reason "OPERATOR" at version 2

  # ---- Completed and Cancelled are final ---------------------------------

  Scenario: A completed request cannot be cancelled
    Given inventory relocates 52 units of "SKU-1" from "WH1-RSV-A07" to "WH1-FWD-A01" at site "WH1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    When I cancel the request for "SKU-1" at site "WH1"
    Then the command fails with "not open"
    And the request for "SKU-1" at site "WH1" is "Completed"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCompleted"

  Scenario: A completed request ignores a second relocation that references it
    Given inventory relocates 52 units of "SKU-1" from "WH1-RSV-A07" to "WH1-FWD-A01" at site "WH1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    When inventory relocates 10 units of "SKU-1" from "WH1-RSV-A07" to "WH1-FWD-A01" at site "WH1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    Then the consumer outcome is "ignored"
    And the request for "SKU-1" at site "WH1" was completed with 52 units moved
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCompleted"

  # ---- which relocation completes a request ------------------------------

  Scenario Outline: A relocation completes a request only with the exact reason REPLENISHMENT
    When inventory relocates 52 units of "SKU-1" from "WH1-RSV-A07" to "WH1-FWD-A01" at site "WH1" with reason "<reason>" referencing "repl-0001"
    Then the consumer outcome is "ignored"
    And the request for "SKU-1" at site "WH1" is "Requested"

    Examples:
      | reason              |
      | replenishment       |
      | Replenishment       |
      | REPLENISH           |
      | REPLENISHMENT_MOVE  |

  Scenario: Only a relocation that is ours is checked for its quantity
    When inventory relocates 0 units of "SKU-1" from "WH1-RSV-A07" to "WH1-FWD-A01" at site "WH1" with reason "SLOTTING" referencing "repl-0001"
    Then the consumer outcome is "ignored"
    When inventory relocates 0 units of "SKU-1" from "WH1-RSV-A07" to "WH1-FWD-A01" at site "WH1" with reason "REPLENISHMENT" referencing "plan-0001"
    Then the consumer outcome is "ignored"
    And the request for "SKU-1" at site "WH1" is "Requested"

  Scenario: A relocation of a single unit completes the request with one unit moved
    When inventory relocates 1 units of "SKU-1" from "WH1-RSV-A07" to "WH1-FWD-A01" at site "WH1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    Then the consumer outcome is "applied"
    And the request for "SKU-1" at site "WH1" was completed with 1 units moved

  # ---- the shape of a request id -----------------------------------------

  Scenario Outline: A request id that does not have the shape of one is refused before any lookup
    When I cancel the request "<id>"
    Then the command fails with "request id must start with repl-"
    And the request for "SKU-1" at site "WH1" is "Requested"

    Examples:
      | id                                                                  |
      | repl-                                                               |
      | REPL-0001                                                           |
      | repl-00 01                                                          |
      | repl-0001/x                                                         |
      | repl-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa   |

  Scenario Outline: A request id of 6 to 64 characters is looked up and an unknown one is not found
    When I cancel the request "<id>"
    Then the command fails with "not found"
    And the request for "SKU-1" at site "WH1" is "Requested"

    Examples:
      | id                                                                 |
      | repl-1                                                             |
      | repl-9999                                                          |
      | repl-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa   |

  # ---- a newer slot plan ---------------------------------------------------

  Scenario: A slot plan that drops one SKU cancels only the request of that SKU
    Given the forward location "WH1-FWD-B01" is registered
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-2" to "WH1-FWD-B01"
    And a policy for "SKU-2" at site "WH1" with min 10 max 60 and multiple 1
    And bin "WH1-FWD-B01" of site "WH1" holds 4 on hand and 0 reserved of "SKU-2"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-2"
    When the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01"
    Then the request for "SKU-2" at site "WH1" was cancelled because "ASSIGNMENT_CHANGED"
    And the request for "SKU-1" at site "WH1" is "Requested"
    And the request for "SKU-1" at site "WH1" is at version 1
    And the last ReplenishmentCancelled fact has reason "ASSIGNMENT_CHANGED" at version 2

  Scenario: A slot plan that keeps an assignment does not evaluate that pick face again
    Given I cancel the request for "SKU-1" at site "WH1"
    And the forward location "WH1-FWD-B01" is registered
    And a policy for "SKU-2" at site "WH1" with min 10 max 60 and multiple 1
    And bin "WH1-FWD-B01" of site "WH1" holds 4 on hand and 0 reserved of "SKU-2"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-2"
    When the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-2" to "WH1-FWD-B01"
    Then there are 1 requests for "SKU-1" at site "WH1"
    And the request for "SKU-1" at site "WH1" is "Cancelled"
    And there are 1 requests for "SKU-2" at site "WH1"
    And the request for "SKU-2" at site "WH1" moves 56 units from "WH1-RSV-A07" to "WH1-FWD-B01" with priority "NORMAL"

  Scenario: A slot plan that moves a SKU into a reserve zone cancels its request and raises no new one
    When the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-RSV-A07"
    Then the request for "SKU-1" at site "WH1" was cancelled because "ASSIGNMENT_CHANGED"
    And there are 1 requests for "SKU-1" at site "WH1"
    And there are 0 pick faces at site "WH1"

  # ---- removing the policy -------------------------------------------------

  Scenario: Removing a policy cancels only the requests of its own site and SKU
    Given the forward location "WH1-FWD-B01" is registered
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-2" to "WH1-FWD-B01"
    And a policy for "SKU-2" at site "WH1" with min 10 max 60 and multiple 1
    And the zone "z-fwd-wh2" of site "WH2" has code "FWD"
    And the zone "z-rsv-wh2" of site "WH2" has code "RSV"
    And the location "WH2-FWD-A01" is registered in zone "z-fwd-wh2"
    And the location "WH2-RSV-A01" is registered in zone "z-rsv-wh2"
    And the approved slot plan of site "WH2" assigns "SKU-1" to "WH2-FWD-A01"
    And a policy for "SKU-1" at site "WH2" with min 10 max 60 and multiple 1
    And bin "WH1-FWD-B01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-2"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-2"
    And bin "WH2-FWD-A01" of site "WH2" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "WH2-RSV-A01" of site "WH2" holds 100 of "SKU-1"
    When I remove the policy of "SKU-1" at site "WH1" at version 1
    Then 1 request was cancelled by the removal
    And the oldest request for "SKU-1" at site "WH1" was cancelled because "POLICY_REMOVED"
    And the request for "SKU-2" at site "WH1" is "Requested"
    And the request for "SKU-1" at site "WH2" is "Requested"
    And the last ReplenishmentCancelled fact has reason "POLICY_REMOVED" at version 2

  Scenario: Removing a policy raises no policy fact and frees the key for a new declaration
    Given I cancel the request for "SKU-1" at site "WH1"
    When I remove the policy of "SKU-1" at site "WH1" at version 1
    Then 0 request was cancelled by the removal
    And the policy of "SKU-1" at site "WH1" does not exist
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCancelled"
    When I declare a policy for "SKU-1" at site "WH1" with min 5 max 20 and multiple 1
    Then the command succeeds
    And the policy of "SKU-1" at site "WH1" is at version 1 with min 5 max 20 and multiple 1
