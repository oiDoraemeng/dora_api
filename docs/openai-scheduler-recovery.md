# Idle API-key account recovery

The advanced scheduler continues to prefer its scored Top-K pool and uses the
remaining eligible accounts as hard-capacity overflow. Two mechanisms prevent
old observations from permanently starving an account after it recovers.

- Error and TTFT observations retain full weight for three minutes. Their
  scheduling confidence then halves every five minutes. Old error rates move
  toward a neutral 10% prior; TTFT factors move toward neutral. TTFT observations
  older than 30 minutes stop defining the latency range. New results incorporate
  the aged prior rather than restoring stale penalties. Sticky escape retains
  raw evidence until a new result arrives.
- At most one in twenty load-balance selections may promote an idle OpenAI
  API-key account outside Top-K. It must have known zero concurrency, no waiting
  requests, the same priority as the preferred pool, and no selection or result
  in the last three minutes. Choose the oldest eligible account first. A local
  atomic reservation permits one recovery request per account in flight and
  at most one start every three minutes, shared across groups in the process.

Recovery uses an ordinary incoming request. It adds no scheduled probe or
synthetic request, and no additional database or Redis operation. The existing
slot acquisition, snapshot and database checks still enforce availability,
model support, cooldowns, capabilities, quota and hard concurrency. Recovery
never changes an existing sticky binding. Compact, image/live requests, pinned
owners, previous-response continuations, and weighted-sticky selection do not
participate. Existing capacity overflow remains available even when a recovery
reservation is already taken.

Successful reservation and slot acquisition emit an info event named
`openai.scheduler_recovery_selected`, with account ID, group ID and model. This
event records dispatch, not successful inference; correlate subsequent request
results before assessing recovery. Reservations and confidence are in memory
and reset on restart. Limits are per process, while hard concurrency remains
enforced by Redis across processes.
