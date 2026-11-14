Feature: Pack-derived replenishment multiple (ADR 0009)
  A policy declared with source PACK_DERIVED takes its multiple from the units
  per pack of the SKU's active CASE level, kept in a local view fed by
  product-master. When the CASE level is superseded the policy re-derives; when
  it is retired and none remains the policy is flagged stale and keeps its last
  multiple. A policy with a typed multiple never changes. Min, max and request
  quantities stay in eaches.

  Background:
    Given site "WH1" has forward bin "WH1-FWD-A01" for "SKU-1" and reserve bins "WH1-RSV-A07"

  # ---- declaring ----------------------------------------------------------

  Scenario: A pack-derived policy takes the units per pack of the active CASE level as its multiple
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    When I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    Then the command succeeds
    And the policy of "SKU-1" at site "WH1" is pack-derived at version 1 with min 10 max 100 multiple 12 and is current
    And the last policy declaration of "SKU-1" at site "WH1" has version 1, multiple 12 and source "PACK_DERIVED"

  Scenario Outline: A pack-derived policy needs a CASE level in force at the moment of the declaration
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "<effective_from>" at version 1
    When I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    Then the command is <verdict>
    And the policy of "SKU-1" at site "WH1" <existence>

    Examples:
      | effective_from       | verdict  | existence      |
      | 2026-10-17T09:00:00Z | accepted | exists         |
      | 2026-10-17T09:00:01Z | refused  | does not exist |

  Scenario Outline: A CASE level end-dated at or before now is no longer in force
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And product-master retires the CASE pack level of "SKU-1" with gtin "14006381333938" effective to "<effective_to>" at version 2
    When I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    Then the command is <verdict>

    Examples:
      | effective_to         | verdict  |
      | 2026-10-17T09:00:00Z | refused  |
      | 2026-10-17T09:00:01Z | accepted |

  Scenario: A SKU with no CASE level cannot get a pack-derived policy
    Given product-master declares the EACH pack level of "SKU-1" as gtin "04006381333931" with 1 units per pack effective "2026-10-01T00:00:00Z" at version 1
    When I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    Then the command fails with "no active CASE pack level"
    And the policy of "SKU-1" at site "WH1" does not exist
    And there are 0 policy declarations

  Scenario: A pack-derived declaration must not type a multiple
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    When I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100 and multiple 6
    Then the command fails with "multiple must be omitted"
    And the policy of "SKU-1" at site "WH1" does not exist

  Scenario: Changing a pack-derived policy keeps it pack-derived at the multiple of the active CASE level
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    When I change the policy of "SKU-1" at site "WH1" from version 1 to pack-derived min 20 max 120
    Then the command succeeds
    And the policy of "SKU-1" at site "WH1" is pack-derived at version 2 with min 20 max 120 multiple 12 and is current

  # ---- following the CASE level ------------------------------------------

  Scenario: A new CASE level re-derives the multiple of every site's pack-derived policy of the SKU and of no other policy
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    And I declare a pack-derived policy for "SKU-1" at site "WH2" with min 20 max 200
    And a policy for "SKU-2" at site "WH1" with min 10 max 100 and multiple 6
    When product-master declares the CASE pack level of "SKU-1" as gtin "24006381333935" with 24 units per pack effective "2026-10-17T09:00:00Z" at version 2
    Then the policy of "SKU-1" at site "WH1" is pack-derived at version 2 with min 10 max 100 multiple 24 and is current
    And the policy of "SKU-1" at site "WH2" is pack-derived at version 2 with min 20 max 200 multiple 24 and is current
    And the policy of "SKU-2" at site "WH1" is typed at version 1 with multiple 6
    And the last policy declaration of "SKU-1" at site "WH2" has version 2, multiple 24 and source "PACK_DERIVED"
    And there are 5 policy declarations

  Scenario: A policy with a typed multiple never changes when pack levels change
    Given a policy for "SKU-1" at site "WH1" with min 10 max 100 and multiple 6
    And product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    When product-master declares the CASE pack level of "SKU-1" as gtin "24006381333935" with 24 units per pack effective "2026-10-17T09:00:00Z" at version 2
    Then the policy of "SKU-1" at site "WH1" is typed at version 1 with multiple 6
    And there are 1 policy declarations

  Scenario: Only the CASE level drives the multiple
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    When product-master declares the PALLET pack level of "SKU-1" as gtin "54006381333933" with 480 units per pack effective "2026-10-17T09:00:00Z" at version 1
    Then the consumer outcome is "applied"
    And the policy of "SKU-1" at site "WH1" is pack-derived at version 1 with min 10 max 100 multiple 12 and is current
    And there are 1 policy declarations

  Scenario: Retiring the only CASE level flags a pack-derived policy stale and keeps its multiple
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    When product-master retires the CASE pack level of "SKU-1" with gtin "14006381333938" effective to "2026-10-10T00:00:00Z" at version 2
    Then the consumer outcome is "applied"
    And the policy of "SKU-1" at site "WH1" is pack-derived and stale with multiple 12

  Scenario Outline: A new CASE level repairs a stale pack-derived policy, whatever its units per pack
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    And product-master retires the CASE pack level of "SKU-1" with gtin "14006381333938" effective to "2026-10-10T00:00:00Z" at version 2
    When product-master declares the CASE pack level of "SKU-1" as gtin "24006381333935" with <units> units per pack effective "2026-10-17T09:00:00Z" at version 3
    Then the policy of "SKU-1" at site "WH1" is pack-derived and current with multiple <units>

    Examples:
      | units |
      | 24    |
      | 12    |

  Scenario: A repeated retirement of the level that already left a policy stale does not change the policy again
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    And product-master retires the CASE pack level of "SKU-1" with gtin "14006381333938" effective to "2026-10-10T00:00:00Z" at version 2
    And I note the version of the policy of "SKU-1" at site "WH1"
    When product-master retires the CASE pack level of "SKU-1" with gtin "14006381333938" effective to "2026-10-11T00:00:00Z" at version 3
    Then the policy of "SKU-1" at site "WH1" is still at the noted version
    And the policy of "SKU-1" at site "WH1" is pack-derived and stale with multiple 12

  Scenario: A late retirement of a superseded GTIN does not end the CASE level that replaced it
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And product-master declares the CASE pack level of "SKU-1" as gtin "24006381333935" with 24 units per pack effective "2026-10-10T00:00:00Z" at version 2
    And I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    When product-master retires the CASE pack level of "SKU-1" with gtin "14006381333938" effective to "2026-10-12T00:00:00Z" at version 3
    Then the consumer outcome is "ignored"
    And the policy of "SKU-1" at site "WH1" is pack-derived and current with multiple 24

  Scenario: The retirement of a level product-master never declared is ignored
    When product-master retires the CASE pack level of "SKU-1" with gtin "14006381333938" effective to "2026-10-10T00:00:00Z" at version 2
    Then the consumer outcome is "ignored"

  # ---- ordering and redelivery -------------------------------------------

  Scenario Outline: A pack level is replaced only by a greater product version
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 2
    When product-master declares the CASE pack level of "SKU-1" as gtin "24006381333935" with 24 units per pack effective "2026-10-01T00:00:00Z" at version <version>
    Then the consumer outcome is "<outcome>"
    When I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    Then the policy of "SKU-1" at site "WH1" is pack-derived and current with multiple <multiple>

    Examples:
      | version | outcome | multiple |
      | 1       | stale   | 12       |
      | 2       | stale   | 12       |
      | 3       | applied | 24       |

  Scenario: A retirement with a version that is not greater is stale and leaves the level in force
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 2
    When product-master retires the CASE pack level of "SKU-1" with gtin "14006381333938" effective to "2026-10-10T00:00:00Z" at version 2
    Then the consumer outcome is "stale"
    When I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    Then the policy of "SKU-1" at site "WH1" is pack-derived and current with multiple 12

  Scenario: A pack-level event delivered twice is claimed once
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    And product-master declares the CASE pack level of "SKU-1" as gtin "24006381333935" with 24 units per pack effective "2026-10-17T09:00:00Z" at version 2
    When the last event is delivered again
    Then the consumer outcome is "duplicate"
    And there are 2 policy declarations

  Scenario Outline: A pack level that can never be applied is invalid
    When product-master declares the <level> pack level of "<sku>" as gtin "<gtin>" with <units> units per pack effective "2026-10-01T00:00:00Z" at version <version>
    Then the command fails with "invalid event"

    Examples:
      | level | sku   | gtin           | units | version |
      | BOX   | SKU-1 | 14006381333938 | 12    | 1       |
      | CASE  |       | 14006381333938 | 12    | 1       |
      | CASE  | SKU-1 |                | 12    | 1       |
      | CASE  | SKU-1 | 14006381333938 | 0     | 1       |
      | CASE  | SKU-1 | 14006381333938 | 12    | 0       |

  Scenario Outline: A pack-level retirement that can never be applied is invalid
    When product-master retires the CASE pack level of "SKU-1" with gtin "<gtin>" effective to "2026-10-10T00:00:00Z" at version <version>
    Then the command fails with "invalid event"

    Examples:
      | gtin           | version |
      |                | 2       |
      | 14006381333938 | 0       |

  # ---- sizing in whole cases ---------------------------------------------

  Scenario Outline: A derived multiple turns the need into whole cases
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    And bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    When bin "WH1-RSV-A07" of site "WH1" holds <reserve> of "SKU-1"
    Then the units requested for "SKU-1" at site "WH1" are <units>

    Examples:
      | reserve | units |
      | 100     | 84    |
      | 50      | 48    |
      | 11      | 0     |

  Scenario: A stale pack-derived policy keeps sizing requests with its last multiple
    Given product-master declares the CASE pack level of "SKU-1" as gtin "14006381333938" with 12 units per pack effective "2026-10-01T00:00:00Z" at version 1
    And I declare a pack-derived policy for "SKU-1" at site "WH1" with min 10 max 100
    And product-master retires the CASE pack level of "SKU-1" with gtin "14006381333938" effective to "2026-10-10T00:00:00Z" at version 2
    And bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    When bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    Then the units requested for "SKU-1" at site "WH1" are 84
