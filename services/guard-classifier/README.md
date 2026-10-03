# Guard classifier

Warden's second, independent line of defense against malicious tool
content: a small fine-tuned encoder that scans tool descriptions and tool
outputs for prompt-injection attempts, served over gRPC with a strict
latency budget. See `internal/guard` (Go client) and `internal/mcpgateway`
for how the gateway calls this, and the root `DECISIONS.md` for the full
story of how this classifier was built and iterated on — it's one of the
more instructive parts of this project to be able to explain, not just the
end result.

## Reproducing from scratch

Model weights are **not** committed (the ONNX export alone is ~260MB,
over GitHub's file size limit, and regenerating from code + a public
dataset is the right practice anyway — see `.gitignore`).

```bash
cd services/guard-classifier
python3.12 -m venv .venv   # or any Python >= 3.10; 3.9 (system default here) is too old for some deps
source .venv/bin/activate
pip install -r requirements.txt

python3 train.py          # fine-tunes DistilBERT, ~1-2 minutes on CPU; prints held-out metrics
python3 export_onnx.py    # exports ./model to ./model/model.onnx
python3 server.py         # serves gRPC on :50051
```

Then, in another terminal, `go run ./cmd/gateway` (from the repo root) will
connect to it at `localhost:50051` by default (`GUARD_CLASSIFIER_ADDR` to
override).

## What's actually in here

- `augment_data.py` — hand-written examples in the tool-description/
  tool-output domain, combined with the public `deepset/prompt-injections`
  dataset. Its docstring explains *why* it exists: two real false-positive
  bugs, both found by actually running the system, not anticipated in
  advance. Worth reading before touching `train.py`.
- `train.py` — fine-tunes `distilbert-base-uncased`, reports metrics on
  three slices (combined / deepset-only / augmented-only) specifically so
  a regression in one domain can't hide behind a good number in another.
- `export_onnx.py` — exports to ONNX. The dynamic-axes configuration here
  is not cosmetic: get it wrong (as the first version of this file did)
  and every inference call runs the full fixed-length compute graph
  regardless of actual input size — a ~25x latency difference in practice,
  see `DECISIONS.md`.
- `server.py` — the gRPC server. The `SessionOptions` here (thread count,
  graph optimization level) were set by measuring, not guessing.
- `benchmark.py` — precision/recall/FPR/latency against the **served**
  ONNX model over real gRPC, not the in-process PyTorch model — this
  measures what's actually deployed, export step included.
- `llm_judge_benchmark.py` — the classifier-vs-LLM-judge comparison the
  original spec asks for. Fully built, deliberately not run: it needs a
  real model provider behind Warden's gateway (still mock-only as of this
  milestone — see milestone 2's DECISIONS.md entries), and running it
  against a provider that can't actually judge text would produce numbers
  that look real but aren't.

## Current numbers (measured, not estimated)

See the root `docs/BENCHMARKS.md` for the full writeup. Headline: 93.6%
accuracy, 100% precision, 86.8% recall, 0% false-positive rate on a
141-example held-out set; p50 7.6ms / p99 36ms per classification, served
over gRPC.
