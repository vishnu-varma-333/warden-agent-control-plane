"""Measures precision, recall, and false-positive rate of the SERVED model
(the ONNX model over gRPC, not the in-process PyTorch model) against the
combined held-out test set, plus per-call latency. This measures what's
actually deployed, including the ONNX export and the gRPC round trip — not
just the training-time numbers, which only reflect the PyTorch model.

Requires: server.py already running (python server.py).
Run: python benchmark.py
"""

import time

import grpc
from datasets import load_dataset, Dataset, concatenate_datasets
from sklearn.metrics import confusion_matrix
from sklearn.model_selection import train_test_split

from augment_data import BENIGN_TOOL_TEXTS, INJECTION_TOOL_TEXTS, SHORT_BENIGN_OUTPUTS, SHORT_INJECTION_OUTPUTS
from guardpb import guard_pb2, guard_pb2_grpc

SEED = 42


def held_out_test_set():
    deepset = load_dataset("deepset/prompt-injections")
    texts = BENIGN_TOOL_TEXTS + INJECTION_TOOL_TEXTS + SHORT_BENIGN_OUTPUTS + SHORT_INJECTION_OUTPUTS
    labels = (
        [0] * len(BENIGN_TOOL_TEXTS) + [1] * len(INJECTION_TOOL_TEXTS)
        + [0] * len(SHORT_BENIGN_OUTPUTS) + [1] * len(SHORT_INJECTION_OUTPUTS)
    )
    _, aug_test_texts, _, aug_test_labels = train_test_split(
        texts, labels, test_size=0.3, random_state=SEED, stratify=labels
    )
    all_texts = list(deepset["test"]["text"]) + aug_test_texts
    all_labels = list(deepset["test"]["label"]) + aug_test_labels
    return all_texts, all_labels


def main():
    texts, labels = held_out_test_set()

    channel = grpc.insecure_channel("localhost:50051")
    stub = guard_pb2_grpc.GuardStub(channel)

    preds = []
    latencies = []
    for text in texts:
        resp = stub.Classify(guard_pb2.ClassifyRequest(text=text))
        preds.append(1 if resp.is_injection else 0)
        latencies.append(resp.latency_ms)

    tn, fp, fn, tp = confusion_matrix(labels, preds).ravel()
    precision = tp / (tp + fp) if (tp + fp) else 0.0
    recall = tp / (tp + fn) if (tp + fn) else 0.0
    fpr = fp / (fp + tn) if (fp + tn) else 0.0
    accuracy = (tp + tn) / len(labels)

    latencies.sort()
    p50 = latencies[len(latencies) // 2]
    p99 = latencies[int(len(latencies) * 0.99)]

    print(f"Held-out set size: {len(labels)}")
    print(f"Confusion matrix: TP={tp} FP={fp} FN={fn} TN={tn}")
    print(f"Accuracy:  {accuracy:.4f}")
    print(f"Precision: {precision:.4f}")
    print(f"Recall:    {recall:.4f}")
    print(f"FPR:       {fpr:.4f}")
    print(f"Latency p50: {p50:.2f}ms   p99: {p99:.2f}ms   max: {max(latencies):.2f}ms")


if __name__ == "__main__":
    main()
