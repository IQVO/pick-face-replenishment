Feature: The forward map only moves forward in time
  SlotPlanApproved carries the FULL forward map of a site plus the instant the
  plan was approved. The service remembers the plan and the instant that
  produced the map it holds. A message approved before that instant is an older
  map arriving late (a replay, a rebalance): applying it would bring back
  assignments a newer plan already dropped. One approved at the same instant is
  the same map again. Both are claimed and changed nothing (ADR 0005).

  Background:
    Given site "SITE-1" has forward bin "F-01" for "SKU-1" and reserve bins "R-01"
    And a policy for "SKU-1" at site "SITE-1" with min 10 max 60 and multiple 1
    And bin "F-01" of site "SITE-1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    And the forward location "F-02" is registered

  Scenario: A map approved before the current one is stale and changes nothing
    When the slot plan "plan-old" of site "SITE-1" approved at "2026-10-17T08:00:00Z" assigns "SKU-1" to "F-02"
    Then the consumer outcome is "stale"
    And the request for "SKU-1" at site "SITE-1" is "Requested"
    And the open request for "SKU-1" at site "SITE-1" has forward bin "F-01"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested"

  Scenario: A map approved at the instant of the current one is an idempotent no-op
    When the slot plan "plan-twin" of site "SITE-1" approved at "2026-10-17T09:00:00Z" assigns "SKU-1" to "F-02"
    Then the consumer outcome is "stale"
    And the open request for "SKU-1" at site "SITE-1" has forward bin "F-01"

  Scenario: A map approved after the current one replaces it
    When the slot plan "plan-new" of site "SITE-1" approved at "2026-10-17T09:30:00Z" assigns "SKU-1" to "F-02"
    Then the consumer outcome is "applied"
    And the oldest request for "SKU-1" at site "SITE-1" was cancelled because "ASSIGNMENT_CHANGED"
    And the open request for "SKU-1" at site "SITE-1" has forward bin "F-02"

  Scenario: A late older map cannot undo a newer one
    Given the slot plan "plan-new" of site "SITE-1" approved at "2026-10-17T09:30:00Z" assigns "SKU-1" to "F-02"
    When the slot plan "plan-mid" of site "SITE-1" approved at "2026-10-17T09:10:00Z" assigns "SKU-1" to "F-01"
    Then the consumer outcome is "stale"
    And the open request for "SKU-1" at site "SITE-1" has forward bin "F-02"

  Scenario: Another site's maps are tracked on their own
    When the slot plan "plan-s2" of site "SITE-2" approved at "2026-10-17T08:00:00Z" assigns "SKU-9" to "G-01"
    Then the consumer outcome is "applied"
