# On This Day

## What this skill does

Shares one genuine, verifiable historical event that happened on today's calendar date, but exactly 100 years ago. Meant for a Teams channel — one short paragraph, not a history lecture.

## Invocation

`@KubexAI onthisday`

No parameter is required. If extra words follow `onthisday`, treat them as an optional topic hint (e.g. `onthisday science`) — prefer a fact matching that topic if a well-documented one exists for the date, otherwise fall back to the best-documented event and don't force the topic.

## Important: use the supplied date — don't guess it

This skill has no clock of its own. The current date is provided to you as context immediately before the instruction, formatted like:

```
[Context: today's date is 2026-08-13 (Thursday), US Eastern.]
```

Always compute "100 years ago" from that exact supplied date (same month and day, year minus 100). Never guess today's date from your own training data, and never state a date other than the one you were given.

## Steps

1. Take the supplied date and subtract exactly 100 years — same month, same day.
2. Recall one genuine, well-documented event you're confident actually happened on that exact date. If nothing well-known happened on the precise date, say so plainly rather than fabricating a plausible-sounding fact or rounding to "around that time."
3. Prefer something with a bit of color or surprise over the most obvious textbook answer, but accuracy always wins over interestingness — never trade a fact you're unsure of for a punchier one.
4. If a topic hint was given (see Invocation), prefer a matching fact if a well-documented one exists for that date; otherwise use the best-documented event regardless of topic.

## Output style

One short paragraph, plain text, chat-friendly — no markdown headers, bullets, or bold. Lead with the date, then the fact, in one to three sentences. No preamble like "Here's an interesting fact" — just the date and the event. Keep it under about 60 words.

## Example

Given context `today's date is 2026-08-13`, the target date is `1926-08-13`. A reply in that shape:

> On August 13, 1926: [one real, verifiable event from that exact date, told in 1-3 sentences].
