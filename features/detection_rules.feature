Feature: Detection rules of the replenishment policy (documentation-derived)
  Rules taken from the context documentation (business-context, use-cases,
  ubiquitous-language, ADR 0002 and ADR 0005) that the first suite did not pin:
  the boundary of the minimum, what counts as a reserve bin, how the source bin
  is ranked, what an unseen bin means, which slots are pick faces and what the
  sweep and the pick-face view report.

  Background:
    Given site "WH1" has forward bin "WH1-FWD-A01" for "SKU-1" and reserve bins "WH1-RSV-A07,WH1-RSV-A08"
    And a policy for "SKU-1" at site "WH1" with min 10 max 60 and multiple 1

  # ---- the trigger: projected <= min -------------------------------------

  Scenario Outline: A pick face is replenished when its available stock reaches the minimum and not above it
    Given bin "WH1-FWD-A01" of site "WH1" holds <on_hand> on hand and <reserved> reserved of "SKU-1"
    When bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    Then there are <requests> requests for "SKU-1" at site "WH1"

    Examples:
      | on_hand | reserved | requests |
      | 10      | 0        | 1        |
      | 11      | 0        | 0        |
      | 15      | 5        | 1        |
      | 16      | 5        | 0        |

  Scenario: A partial request leaves the face below its minimum but no second request is raised while it is open
    Given bin "WH1-FWD-A01" of site "WH1" holds 0 on hand and 0 reserved of "SKU-1"
    When bin "WH1-RSV-A07" of site "WH1" holds 5 of "SKU-1"
    Then the request for "SKU-1" at site "WH1" moves 5 units from "WH1-RSV-A07" to "WH1-FWD-A01" with priority "URGENT"
    When bin "WH1-RSV-A08" of site "WH1" holds 100 of "SKU-1"
    Then there are 1 requests for "SKU-1" at site "WH1"
    And the pick face "WH1-FWD-A01" of site "WH1" has status "BELOW_MIN"
    When I evaluate site "WH1"
    Then the sweep raised 0, found 1 already open, 0 healthy, 0 without policy and 0 without source

  Scenario: Once a partial request closes the next detection raises the rest
    Given bin "WH1-FWD-A01" of site "WH1" holds 0 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 5 of "SKU-1"
    And bin "WH1-RSV-A08" of site "WH1" holds 100 of "SKU-1"
    When inventory relocates 5 units of "SKU-1" from "WH1-RSV-A07" to "WH1-FWD-A01" at site "WH1" with reason "REPLENISHMENT" referencing the request for "SKU-1"
    Then the request for "SKU-1" at site "WH1" was completed with 5 units moved
    When bin "WH1-FWD-A01" of site "WH1" holds 5 on hand and 0 reserved of "SKU-1"
    Then there are 2 requests for "SKU-1" at site "WH1"
    And the request for "SKU-1" at site "WH1" moves 55 units from "WH1-RSV-A08" to "WH1-FWD-A01" with priority "NORMAL"

  Scenario: A pick face whose level never arrived reads as empty and its first request is URGENT
    When bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    Then the request for "SKU-1" at site "WH1" moves 60 units from "WH1-RSV-A07" to "WH1-FWD-A01" with priority "URGENT"

  Scenario: Changing a policy takes effect at the next detection and sizes the request with the new bounds
    Given bin "WH1-FWD-A01" of site "WH1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    When I change the policy of "SKU-1" at site "WH1" from version 1 to min 50 max 80 and multiple 1
    Then the command succeeds
    And there are 0 requests for "SKU-1" at site "WH1"
    And the pick face "WH1-FWD-A01" of site "WH1" has status "BELOW_MIN"
    When I evaluate site "WH1"
    Then the sweep raised 1, found 0 already open, 0 healthy, 0 without policy and 0 without source
    And the last ReplenishmentRequested fact moves 40 units from "WH1-RSV-A07" to "WH1-FWD-A01" with priority "NORMAL" sized by policy min 50 max 80 at version 1

  Scenario Outline: A pick face reads BELOW_MIN from the moment its projected level reaches the minimum
    Given bin "WH1-FWD-A01" of site "WH1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    When I change the policy of "SKU-1" at site "WH1" from version 1 to min <min> max 80 and multiple 1
    Then the pick face "WH1-FWD-A01" of site "WH1" has status "<status>"

    Examples:
      | min | status    |
      | 39  | OK        |
      | 40  | BELOW_MIN |
      | 41  | BELOW_MIN |

  # ---- the source bin: fullest-reserve-bin-v1 ----------------------------

  Scenario: Reserve bins with the same usable stock are tied to the smallest bin code
    Given bin "WH1-FWD-A01" of site "WH1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A08" of site "WH1" holds 50 of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 50 of "SKU-1"
    When bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    Then the request for "SKU-1" at site "WH1" moves 50 units from "WH1-RSV-A07" to "WH1-FWD-A01" with priority "NORMAL"

  Scenario: The reserve bin with the most usable stock supplies the request, not the one with the most on hand
    Given bin "WH1-FWD-A01" of site "WH1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 on hand and 80 reserved of "SKU-1"
    And bin "WH1-RSV-A08" of site "WH1" holds 30 of "SKU-1"
    When bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    Then the request for "SKU-1" at site "WH1" moves 30 units from "WH1-RSV-A08" to "WH1-FWD-A01" with priority "NORMAL"

  Scenario Outline: A reserve bin needs at least one usable unit to supply a request
    Given bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    When bin "WH1-RSV-A07" of site "WH1" holds <on_hand> on hand and <reserved> reserved of "SKU-1"
    Then there are <requests> requests for "SKU-1" at site "WH1"

    Examples:
      | on_hand | reserved | requests |
      | 6       | 5        | 1        |
      | 5       | 5        | 0        |

  Scenario Outline: The quantity is capped at the source's usable stock and rounded down to the multiple again
    Given a policy for "SKU-2" at site "WH1" with min 10 max 60 and multiple 6
    And the forward location "WH1-FWD-B01" is registered
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-2" to "WH1-FWD-B01"
    And bin "WH1-FWD-B01" of site "WH1" holds 4 on hand and 0 reserved of "SKU-2"
    When bin "WH1-RSV-A07" of site "WH1" holds <source> of "SKU-2"
    Then the units requested for "SKU-2" at site "WH1" are <units>
    And the pick face "WH1-FWD-B01" of site "WH1" has status "<status>"

    Examples:
      | source | units | status    |
      | 20     | 18    | OK        |
      | 5      | 0     | NO_SOURCE |

  Scenario Outline: Only a Storage location in a reserve zone of the site can supply a request
    Given the zone "z-stg" of site "WH1" has code "STG"
    And the zone "z-rsv-wh2" of site "WH2" has code "RSV"
    And bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    When a candidate bin "WH1-CAND-01" at site "WH1" in zone "<zone>" with role "<role>" holds 100 of "SKU-1"
    Then there are <requests> requests for "SKU-1" at site "WH1"
    And the pick face "WH1-FWD-A01" of site "WH1" has status "<status>"

    Examples:
      | zone    | role    | requests | status    |
      | z-rsv   | Storage | 1        | OK        |
      | z-rsv   |         | 1        | OK        |
      | z-rsv   | Staging | 0        | NO_SOURCE |
      | z-stg   | Storage | 0        | NO_SOURCE |
      | z-late  | Storage | 0        | NO_SOURCE |
      | z-rsv-wh2 | Storage | 0      | NO_SOURCE |

  Scenario: A decommissioned reserve bin no longer supplies requests
    Given bin "WH1-FWD-A01" of site "WH1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    And the location "WH1-RSV-A07" is decommissioned
    When bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    Then there are 0 requests for "SKU-1" at site "WH1"
    And the pick face "WH1-FWD-A01" of site "WH1" has status "NO_SOURCE"

  Scenario: Reserve stock of another site is never a source
    Given the zone "z-rsv-wh2" of site "WH2" has code "RSV"
    And the location "WH2-RSV-A01" is registered in zone "z-rsv-wh2"
    And bin "WH2-RSV-A01" of site "WH2" holds 100 of "SKU-1"
    When bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    Then there are 0 requests for "SKU-1" at site "WH1"
    And the pick face "WH1-FWD-A01" of site "WH1" has status "NO_SOURCE"

  Scenario: A zone registered again under another code reclassifies its bins
    Given bin "WH1-FWD-A01" of site "WH1" holds 40 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    And the zone "z-rsv" of site "WH1" has code "BULK"
    When bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    Then there are 0 requests for "SKU-1" at site "WH1"
    And the pick face "WH1-FWD-A01" of site "WH1" has status "NO_SOURCE"

  # ---- which slots are pick faces ----------------------------------------

  Scenario: A slot whose zone has not arrived counts as a pick face until the zone says otherwise
    Given the location "WH1-FWD-B01" is registered in zone "z-late"
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-3" to "WH1-FWD-B01", "SKU-4" to "WH1-FWD-C01"
    When I list the pick faces of site "WH1"
    Then the pick faces are "WH1-FWD-A01,WH1-FWD-B01,WH1-FWD-C01"
    When the zone "z-late" of site "WH1" has code "BULK"
    And I list the pick faces of site "WH1"
    Then the pick faces are "WH1-FWD-A01,WH1-FWD-C01"

  Scenario: Decommissioning is irreversible, even for a location decommissioned before it was registered
    Given the location "WH1-FWD-B01" is decommissioned
    And the location "WH1-FWD-B01" is registered in zone "z-fwd"
    And the location "WH1-FWD-A01" is decommissioned
    And the location "WH1-FWD-A01" is registered in zone "z-fwd"
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-3" to "WH1-FWD-B01"
    When I list the pick faces of site "WH1"
    Then the pick faces are ""

  # ---- the sweep and the pick-face view ----------------------------------

  Scenario: A sweep reports every verdict and raises requests in forward-bin order
    Given bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    And the forward location "WH1-FWD-B01" is registered
    And the forward location "WH1-FWD-C01" is registered
    And the forward location "WH1-FWD-D01" is registered
    And the forward location "WH1-FWD-E01" is registered
    And the forward location "WH1-FWD-F01" is registered
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-2" to "WH1-FWD-B01", "SKU-3" to "WH1-FWD-C01", "SKU-4" to "WH1-FWD-D01", "SKU-5" to "WH1-FWD-E01", "SKU-6" to "WH1-FWD-F01"
    And bin "WH1-FWD-B01" of site "WH1" holds 4 on hand and 0 reserved of "SKU-2"
    And bin "WH1-FWD-C01" of site "WH1" holds 40 on hand and 0 reserved of "SKU-3"
    And bin "WH1-FWD-D01" of site "WH1" holds 1 on hand and 0 reserved of "SKU-4"
    And bin "WH1-FWD-E01" of site "WH1" holds 2 on hand and 0 reserved of "SKU-5"
    And bin "WH1-FWD-F01" of site "WH1" holds 3 on hand and 0 reserved of "SKU-6"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-2"
    And bin "WH1-RSV-A08" of site "WH1" holds 100 of "SKU-6"
    And a policy for "SKU-2" at site "WH1" with min 10 max 60 and multiple 1
    And a policy for "SKU-3" at site "WH1" with min 10 max 60 and multiple 1
    And a policy for "SKU-5" at site "WH1" with min 10 max 60 and multiple 1
    And a policy for "SKU-6" at site "WH1" with min 10 max 60 and multiple 1
    When I evaluate site "WH1"
    Then the sweep evaluated 6 pick faces
    And the sweep raised 2, found 1 already open, 1 healthy, 1 without policy and 1 without source
    And the sweep raised the requests "repl-0002,repl-0003"
    And the request "repl-0002" moves 56 units of "SKU-2" from "WH1-RSV-A07" to "WH1-FWD-B01"
    And the request "repl-0003" moves 57 units of "SKU-6" from "WH1-RSV-A08" to "WH1-FWD-F01"
    When I evaluate site "WH1"
    Then the sweep evaluated 6 pick faces
    And the sweep raised 0, found 3 already open, 1 healthy, 1 without policy and 1 without source

  Scenario: A sweep evaluates only the slots that are still pick faces
    Given the forward location "WH1-FWD-B01" is registered
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-2" to "WH1-FWD-B01", "SKU-3" to "WH1-RSV-A07"
    And the location "WH1-FWD-B01" is decommissioned
    When I evaluate site "WH1"
    Then the sweep evaluated 1 pick faces
    And the sweep raised 0, found 0 already open, 0 healthy, 0 without policy and 1 without source

  Scenario: The pick-face view shows the level, the policy bounds and the open request
    Given the forward location "WH1-FWD-B01" is registered
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-3" to "WH1-FWD-B01"
    And bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 3 reserved of "SKU-1"
    When bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    Then the pick face "WH1-FWD-A01" of site "WH1" shows 8 on hand, 3 reserved and 5 available
    And the pick face "WH1-FWD-A01" of site "WH1" shows policy min 10 and max 60
    And the pick face "WH1-FWD-A01" of site "WH1" shows the open request "repl-0001"
    And the pick face "WH1-FWD-B01" of site "WH1" shows no policy bounds
    And the pick face "WH1-FWD-B01" of site "WH1" shows no open request

  # ---- manual requests ---------------------------------------------------

  Scenario: A manual request for an empty pick face is URGENT
    Given bin "WH1-FWD-A01" of site "WH1" holds 0 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    And I cancel the request for "SKU-1" at site "WH1"
    When I request a replenishment of 20 units of "SKU-1" at site "WH1"
    Then the command succeeds
    And the request for "SKU-1" at site "WH1" moves 20 units from "WH1-RSV-A07" to "WH1-FWD-A01" with priority "URGENT"

  Scenario Outline: A manual quantity cannot exceed the usable stock of the chosen source
    Given bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 30 of "SKU-1"
    And I cancel the request for "SKU-1" at site "WH1"
    When I request a replenishment of <quantity> units of "SKU-1" at site "WH1"
    Then the command is <verdict>
    And the units requested for "SKU-1" at site "WH1" are <units>

    Examples:
      | quantity | verdict  | units |
      | 30       | accepted | 30    |
      | 31       | refused  | 0     |

  Scenario: A manual request refused because one is open names the open request
    Given bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    When I request a replenishment of 5 units of "SKU-1" at site "WH1"
    Then the command fails with "request repl-0001 is open"
    When I request a replenishment of "SKU-1" at site "WH1"
    Then the command fails with "request repl-0001 is open"
    And there are 1 requests for "SKU-1" at site "WH1"
