"""The guard classifier service: ONNX Runtime does the actual inference,
exposed two ways —

  - gRPC (:50051): the hot-path interface the Go gateway calls on every
    tool description/output (internal/guard). Low overhead, per the
    original spec's reasoning for choosing gRPC for this call.
  - FastAPI (:8000): health check + a manual /classify endpoint for
    debugging and testing without a gRPC client — curl-able, used by
    humans and tooling, not the gateway's hot path.

Both wrap the same Model — one load of the tokenizer/ONNX session, two
ways to call it, so there's one place that defines how a text turns into a
(is_injection, confidence) result, not two that could drift apart.
"""

import logging
import time
from concurrent import futures

import grpc
import numpy as np
import onnxruntime as ort
import uvicorn
from fastapi import FastAPI
from pydantic import BaseModel
from transformers import AutoTokenizer

from guardpb import guard_pb2, guard_pb2_grpc

MODEL_DIR = "./model"
ONNX_PATH = f"{MODEL_DIR}/model.onnx"
MAX_LENGTH = 128
GRPC_ADDR = "[::]:50051"
HTTP_ADDR = ("0.0.0.0", 8000)

logging.basicConfig(level=logging.INFO)
log = logging.getLogger("guard-classifier")


class Model:
    def __init__(self):
        log.info("loading tokenizer and ONNX session from %s", MODEL_DIR)
        self.tokenizer = AutoTokenizer.from_pretrained(MODEL_DIR)
        # Found via direct benchmarking, not assumed (see DECISIONS.md):
        # padding every input out to a fixed max_length forces the full
        # 128-token compute graph on every call regardless of actual input
        # size, which measured ~123ms/call - using the real (shorter)
        # sequence length instead measured ~4-5ms/call on typical inputs,
        # a ~25x difference. intra_op_num_threads=4 (not 1) and full graph
        # optimization were the other two measured factors; this
        # combination is what the hot-path latency budget actually needs.
        opts = ort.SessionOptions()
        opts.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
        opts.intra_op_num_threads = 4
        self.session = ort.InferenceSession(ONNX_PATH, sess_options=opts, providers=["CPUExecutionProvider"])
        log.info("ready")

    def classify(self, text: str):
        start = time.perf_counter()

        # No padding: let each input use its own real length (see the
        # comment above for why this is the dominant latency factor).
        # max_length is still an upper bound via truncation, so a
        # pathologically long tool output can't blow the latency budget.
        encoded = self.tokenizer(text, truncation=True, max_length=MAX_LENGTH, return_tensors="np")
        logits = self.session.run(
            ["logits"],
            {
                "input_ids": encoded["input_ids"].astype(np.int64),
                "attention_mask": encoded["attention_mask"].astype(np.int64),
            },
        )[0]

        # softmax over the 2 logits (benign, injection)
        exp = np.exp(logits - np.max(logits, axis=-1, keepdims=True))
        probs = exp / exp.sum(axis=-1, keepdims=True)
        predicted = int(np.argmax(probs, axis=-1)[0])
        confidence = float(probs[0][predicted])

        latency_ms = (time.perf_counter() - start) * 1000.0
        return predicted == 1, confidence, latency_ms


class GuardServicer(guard_pb2_grpc.GuardServicer):
    def __init__(self, model: Model):
        self.model = model

    def Classify(self, request, context):
        is_injection, confidence, latency_ms = self.model.classify(request.text)
        return guard_pb2.ClassifyResponse(is_injection=is_injection, confidence=confidence, latency_ms=latency_ms)


def start_grpc(model: Model) -> grpc.Server:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=8))
    guard_pb2_grpc.add_GuardServicer_to_server(GuardServicer(model), server)
    server.add_insecure_port(GRPC_ADDR)
    server.start()  # non-blocking: serves on its own thread pool
    log.info("gRPC listening on %s", GRPC_ADDR)
    return server


class ClassifyHTTPRequest(BaseModel):
    text: str


class ClassifyHTTPResponse(BaseModel):
    is_injection: bool
    confidence: float
    latency_ms: float


def build_http_app(model: Model) -> FastAPI:
    app = FastAPI(title="guard-classifier")

    @app.get("/healthz")
    def healthz():
        return {"status": "ok"}

    @app.post("/classify", response_model=ClassifyHTTPResponse)
    def classify(req: ClassifyHTTPRequest):
        is_injection, confidence, latency_ms = model.classify(req.text)
        return ClassifyHTTPResponse(is_injection=is_injection, confidence=confidence, latency_ms=latency_ms)

    return app


def serve():
    model = Model()
    grpc_server = start_grpc(model)
    app = build_http_app(model)

    # gRPC already runs on its own thread pool (server.start() above is
    # non-blocking); uvicorn.run blocks this thread, which is what keeps
    # the process alive. A clean shutdown on Ctrl+C/SIGTERM stops both.
    try:
        uvicorn.run(app, host=HTTP_ADDR[0], port=HTTP_ADDR[1], log_level="info")
    finally:
        grpc_server.stop(grace=5)


if __name__ == "__main__":
    serve()
