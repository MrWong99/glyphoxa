---
parent: Design Documents
nav_order: 5
---

> *This document is derived from the Glyphoxa Design Document v0.2*

# ⚠️ EXPERIMENTAL: Dual-Model Sentence Cascade

> **This is a novel technique not implemented in any known production system.** It is inspired by speculative decoding and model cascading research but operates at the sentence level rather than the token or request level. Significant prototyping is required to validate coherence, latency gains, and the conditions under which it outperforms a single-model approach.

## The Insight

Humans don't form their entire response before they start speaking. They begin with a quick reaction ("Hmm, well..." or "Ah, the goblins!") while their slower deliberative thinking assembles the full answer. The Dual-Model Sentence Cascade mimics this pattern for NPC voice responses.

**The goal:** reduce perceived latency to under 600ms by starting TTS playback with a fast model's opening sentence, while a stronger model generates the substantive continuation in parallel.

## How It Works

1. **Player finishes speaking.** STT finalizes the transcript.
2. **Fast model generates the opener.** A small, fast model (GPT-4o-mini, Haiku, Gemini Flash) receives the prompt and generates only the first sentence. This model is optimized for time-to-first-token (~200ms).
3. **TTS starts immediately.** The first sentence streams to TTS. The NPC's voice starts playing within ~500ms total mouth-to-ear.
4. **Strong model continues.** While TTS plays the opener, the strong model (Sonnet, GPT-4o, Gemini Pro) receives the same prompt plus the fast model's first sentence as a forced prefix. It generates the continuation from that point.
5. **Seamless handoff.** By the time the first sentence finishes playing (~2–3 seconds), the strong model's continuation is already synthesized and queued. The listener hears a single, continuous NPC utterance.

## Relationship to Existing Research

| Technique | Level | Goal | How Glyphoxa's Approach Differs |
|---|---|---|---|
| Speculative Decoding | Token-level | Speed up a single model by drafting tokens with a small model and verifying with the target | Operates within a single model's output. Guarantees identical output distribution. Does not mix two models' reasoning. |
| Model Cascading | Request-level | Try the small model first; escalate to the large model if confidence is low | Either/or: the response comes from one model or the other, never both in sequence. |
| Speculative Cascading (Google) | Token-level | Combine speculative decoding with cascading for cost-quality tradeoffs | Still token-level verification. Does not produce a blended sentence-level response. |
| **Sentence Cascade (ours)** | Sentence-level | Start speaking immediately with a fast model, then hand off to a strong model for continuation | The response is a blend: sentence 1 from the fast model, sentences 2+ from the strong model. No verification step. The strong model treats the opener as a forced prefix. |

## Risks and Mitigations

**Coherence risk:** The strong model might want to take the response in a different direction than the fast model's opener.
- *Mitigation:* Instruct the fast model to generate only a brief, character-appropriate opening reaction ("The blacksmith strokes his beard thoughtfully...") rather than a substantive claim. System prompt: "Generate only a brief, in-character opening reaction. Do not reveal key information in the first sentence."

**Tone mismatch:** Two models may have slightly different voice/style.
- *Mitigation:* Strong NPC personality directives in the system prompt (shared by both models) plus the forced prefix alignment tend to smooth this out.

**Unnecessary overhead for short responses:** If the NPC just needs to say "Hello, traveler," the cascade adds unnecessary complexity.
- *Mitigation:* Use a response-length estimator. If the fast model's complete response is one sentence or fewer, skip the strong model entirely.

**Cost:** Running two models per utterance approximately doubles LLM cost for that turn.
- *Mitigation:* Only activate the cascade for interactions where the DM has flagged the NPC as "high-importance" or where the conversation topic is complex. Simple NPCs use a single fast model.

## When to Use It

The sentence cascade is **not** a universal optimization. It's most valuable when:

- **Latency-critical NPCs:** A villain's dramatic monologue, a quest-giver's exposition, a tense negotiation. The sub-600ms voice onset creates dramatic immediacy.
- **Complex questions:** Player asks a deep lore question. The fast model buys time ("Ah, you want to know about the Shadowfell...") while the strong model assembles a thorough answer.

**NOT for:** Simple greetings, one-word responses, combat callouts, or any interaction where a single fast model is sufficient. The orchestrator should route these to a single model to save cost and reduce complexity. See [NPC Agents: cascade_mode](06-npc-agents.md).

---

**See also:** [Architecture](01-architecture.md) · [NPC Agents](06-npc-agents.md) · [Technology](07-technology.md)
