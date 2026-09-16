# Agent Development Rules

## Exercise Validity

Every new or materially changed exercise must be tested as if an engineer were
learning from it, not only through unit tests or UI smoke tests. Before the
change is committed, the agent must:

1. Start from a clean exercise reset and follow the learner-facing instructions
   using the real toolbox, metrics, and runtime controls.
2. Complete the intended investigation and mitigation path, then verify that
   the exercise passes only after its declared success criteria hold over the
   required observation window.
3. Simulate at least one plausible but incorrect diagnosis or intervention and
   verify that the exercise gives useful feedback, keeps invalid controls
   locked or rejects the action, and cannot be graded as passed.
4. Reset the exercise and repeat the correct path so the result is independent
   of stale metrics, state, or history from the wrong attempt.
5. Record the commands, actions, waiting period, expected result, and observed
   result in the change summary or verification notes.

An exercise is not considered complete when only its YAML parses or its
endpoint returns HTTP 200. The learner simulation must cover the complete
observe, diagnose, act, verify, and reset lifecycle. Any failure discovered by
the simulation must be fixed and retested before committing and pushing.

When the exercise uses asynchronous metrics or alert windows, the simulation
must wait for the documented evaluation window rather than relying on a single
instantaneous sample.
