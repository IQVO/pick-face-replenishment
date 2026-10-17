Feature: Replenishment policies
  The min/max rule of a pick face is declared per site and SKU. Changing it needs
  the current version, and removing it cancels the requests it had raised.

  Scenario: Declaring a policy creates it at version 1 and publishes the analytics fact
    When I declare a policy for "SKU-1" at site "SITE-1" with min 10 max 60 and multiple 6
    Then the command succeeds
    And the policy of "SKU-1" at site "SITE-1" is at version 1 with min 10 max 60 and multiple 6
    And the published events are "ReplenishmentPolicyDeclared"

  Scenario: A multiple of zero in the command falls back to 1
    When I declare a policy for "SKU-1" at site "SITE-1" with min 0 max 5 and multiple 0
    Then the policy of "SKU-1" at site "SITE-1" is at version 1 with min 0 max 5 and multiple 1

  Scenario: Declaring a policy that exists without the current version is refused
    Given a policy for "SKU-1" at site "SITE-1" with min 10 max 60 and multiple 1
    When I declare a policy for "SKU-1" at site "SITE-1" with min 5 max 30 and multiple 1
    Then the command fails with "already exists"
    And the policy of "SKU-1" at site "SITE-1" is at version 1 with min 10 max 60 and multiple 1

  Scenario: Changing a policy with the current version bumps its version
    Given a policy for "SKU-1" at site "SITE-1" with min 10 max 60 and multiple 1
    When I change the policy of "SKU-1" at site "SITE-1" from version 1 to min 5 max 30 and multiple 2
    Then the command succeeds
    And the policy of "SKU-1" at site "SITE-1" is at version 2 with min 5 max 30 and multiple 2
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentPolicyDeclared"

  Scenario: Changing a policy with a stale version is refused
    Given a policy for "SKU-1" at site "SITE-1" with min 10 max 60 and multiple 1
    When I change the policy of "SKU-1" at site "SITE-1" from version 7 to min 5 max 30 and multiple 2
    Then the command fails with "concurrent modification"
    And the policy of "SKU-1" at site "SITE-1" is at version 1 with min 10 max 60 and multiple 1

  Scenario: Changing a policy nobody declared is a version conflict
    When I change the policy of "SKU-1" at site "SITE-1" from version 1 to min 5 max 30 and multiple 2
    Then the command fails with "concurrent modification"

  Scenario: The minimum must stay below the maximum
    When I declare a policy for "SKU-1" at site "SITE-1" with min 60 max 10 and multiple 1
    Then the command fails with "minQty must be at least 0 and below maxQty"
    And the published events are ""

  Scenario: Removing a policy cancels the open requests of its SKU
    Given site "SITE-1" has forward bin "F-01" for "SKU-1" and reserve bins "R-01"
    And a policy for "SKU-1" at site "SITE-1" with min 10 max 60 and multiple 1
    And bin "F-01" of site "SITE-1" holds 8 on hand and 3 reserved of "SKU-1"
    And bin "R-01" of site "SITE-1" holds 100 of "SKU-1"
    When I remove the policy of "SKU-1" at site "SITE-1" at version 1
    Then the command succeeds
    And 1 request was cancelled by the removal
    And the request for "SKU-1" at site "SITE-1" was cancelled because "POLICY_REMOVED"
    And the published events are "ReplenishmentPolicyDeclared,ReplenishmentRequested,ReplenishmentCancelled"
    And the pick face "F-01" of site "SITE-1" has status "NO_POLICY"

  Scenario: Removing a policy with a stale version changes nothing
    Given a policy for "SKU-1" at site "SITE-1" with min 10 max 60 and multiple 1
    When I remove the policy of "SKU-1" at site "SITE-1" at version 3
    Then the command fails with "concurrent modification"
    And the policy of "SKU-1" at site "SITE-1" is at version 1 with min 10 max 60 and multiple 1

  Scenario: Policies are listed a page at a time in site and SKU order
    Given a policy for "SKU-2" at site "SITE-1" with min 1 max 9 and multiple 1
    And a policy for "SKU-1" at site "SITE-2" with min 1 max 9 and multiple 1
    And a policy for "SKU-1" at site "SITE-1" with min 1 max 9 and multiple 1
    When I list policies with a page size of 2
    Then the listed policies are "SITE-1/SKU-1,SITE-1/SKU-2"
    And there is a next page
    When I list the next page of policies
    Then the listed policies are "SITE-2/SKU-1"
    And there is no next page
