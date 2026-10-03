"""Exports the fine-tuned model (./model, from train.py) to ONNX for fast
CPU inference in the serving path. Run: python export_onnx.py
"""

import torch
from transformers import AutoTokenizer, AutoModelForSequenceClassification

MODEL_DIR = "./model"
ONNX_PATH = "./model/model.onnx"


def main():
    tokenizer = AutoTokenizer.from_pretrained(MODEL_DIR)
    model = AutoModelForSequenceClassification.from_pretrained(MODEL_DIR)
    model.eval()

    dummy = tokenizer("example input for tracing the export graph", return_tensors="pt", padding="max_length", max_length=128)

    torch.onnx.export(
        model,
        (dummy["input_ids"], dummy["attention_mask"]),
        ONNX_PATH,
        input_names=["input_ids", "attention_mask"],
        output_names=["logits"],
        dynamic_axes={
            "input_ids": {0: "batch", 1: "sequence"},
            "attention_mask": {0: "batch", 1: "sequence"},
            "logits": {0: "batch"},
        },
        opset_version=14,
    )
    print(f"Exported ONNX model to {ONNX_PATH}")


if __name__ == "__main__":
    main()
