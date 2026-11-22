Feature: Policy and read rules (documentation-derived)
  Policy invariants, the filters of the lists and the validation of their
  parameters (use cases 1 to 5, 8 and 9; troubleshooting problem types).

  Background:
    Given site "WH1" has forward bin "WH1-FWD-A01" for "SKU-1" and reserve bins "WH1-RSV-A07"

  # ---- the policy invariants ---------------------------------------------

  Scenario Outline: A policy needs a minimum of at least 0 strictly below its maximum and a multiple of at least 1
    When I declare a policy for "SKU-1" at site "WH1" with min <min> max <max> and multiple <multiple>
    Then the command is <verdict>
    And the policy of "SKU-1" at site "WH1" <existence>
    And there are <declared> policy declarations

    Examples:
      | min | max | multiple | verdict  | existence      | declared |
      | 0   | 1   | 1        | accepted | exists         | 1        |
      | 9   | 10  | 1        | accepted | exists         | 1        |
      | 10  | 10  | 1        | refused  | does not exist | 0        |
      | -1  | 10  | 1        | refused  | does not exist | 0        |
      | 5   | 10  | -3       | refused  | does not exist | 0        |

  Scenario: A policy that was never declared is not found
    When I get the policy of "SKU-9" at site "WH1"
    Then the command fails with "not found"
    When I remove the policy of "SKU-9" at site "WH1" at version 1
    Then the command fails with "not found"

  Scenario: Policies can be filtered by site and by SKU
    Given a policy for "SKU-1" at site "WH1" with min 10 max 60 and multiple 1
    And a policy for "SKU-2" at site "WH1" with min 10 max 60 and multiple 1
    And a policy for "SKU-1" at site "WH2" with min 10 max 60 and multiple 1
    When I list policies of site "WH1"
    Then the listed policies are "WH1/SKU-1,WH1/SKU-2"
    When I list policies of sku "SKU-1"
    Then the listed policies are "WH1/SKU-1,WH2/SKU-1"

  # ---- list parameters ---------------------------------------------------

  Scenario Outline: A list limit is accepted from 1 to 500
    When I list the requests with the limit <limit>
    Then the command is <verdict>

    Examples:
      | limit | verdict  |
      | 1     | accepted |
      | 500   | accepted |
      | 501   | refused  |
      | -1    | refused  |

  Scenario: Every list refuses a limit outside 1 to 500 with the same error
    When I list the requests with the limit 501
    Then the command fails with "limit must be between 1 and 500"
    When I list the pick faces of site "WH1" with the limit 501
    Then the command fails with "limit must be between 1 and 500"
    When I list policies with the limit 501
    Then the command fails with "limit must be between 1 and 500"

  Scenario: Every list refuses a cursor this API did not issue
    When I list the requests with the cursor "not a cursor!"
    Then the command fails with "cursor is not a value returned by this API"
    When I list the pick faces of site "WH1" with the cursor "not a cursor!"
    Then the command fails with "cursor is not a value returned by this API"
    When I list policies with the cursor "not a cursor!"
    Then the command fails with "cursor is not a value returned by this API"
    When I list policies with the cursor "bm9wZQ"
    Then the command fails with "cursor is not a value returned by this API"

  Scenario: A request state and a pick-face status outside the documented values are refused
    When I list the requests of site "WH1" in state "Open"
    Then the command fails with "state must be one of Requested, Completed, Cancelled"
    When I list the pick faces of site "WH1" with status "LOW"
    Then the command fails with "status must be one of BELOW_MIN, OK, NO_SOURCE, NO_POLICY"

  Scenario: A page that holds exactly the last items has no next page
    Given a policy for "SKU-1" at site "WH1" with min 10 max 60 and multiple 1
    And a policy for "SKU-2" at site "WH1" with min 10 max 60 and multiple 1
    And the forward location "WH1-FWD-B01" is registered
    And the approved slot plan of site "WH1" assigns "SKU-1" to "WH1-FWD-A01", "SKU-2" to "WH1-FWD-B01"
    And bin "WH1-FWD-A01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-1"
    And bin "WH1-FWD-B01" of site "WH1" holds 8 on hand and 0 reserved of "SKU-2"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-1"
    And bin "WH1-RSV-A07" of site "WH1" holds 100 of "SKU-2"
    When I list policies with a page size of 2
    Then the listed policies are "WH1/SKU-1,WH1/SKU-2"
    And there is no next page
    When I list the requests of site "WH1" with a page size of 2
    Then the listed requests are "repl-0002,repl-0001"
    And there is no next page
    When I list the pick faces of site "WH1" with a page size of 2
    Then the pick faces are "WH1-FWD-A01,WH1-FWD-B01"
    And there is no next page
