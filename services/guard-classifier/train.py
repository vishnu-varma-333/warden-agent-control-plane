"""Fine-tunes a small encoder (DistilBERT) for binary prompt-injection
classification.

Training data is the public deepset/prompt-injections dataset PLUS a small
set of tool-description/tool-output-domain examples (augment_data.py). The
augmentation exists because a classifier trained on deepset alone — mostly
conversational chat text — learned "imperative sentence" as a proxy for
"injection," which fails on tool descriptions (imperative by genre
regardless of intent). See augment_data.py's docstring and DECISIONS.md for
the full story; this script reports metrics on three slices (combined,
deepset-only, augmented-only) specifically so that failure mode is visible
in the numbers, not just fixed silently.

Run: python train.py
Produces: ./model/ (a Hugging Face checkpoint, consumed by export_onnx.py)
"""

import numpy as np
from datasets import load_dataset, Dataset, concatenate_datasets, DatasetDict
from sklearn.metrics import precision_recall_fscore_support, accuracy_score
from sklearn.model_selection import train_test_split
from transformers import (
    AutoTokenizer,
    AutoModelForSequenceClassification,
    Trainer,
    TrainingArguments,
)

from augment_data import BENIGN_TOOL_TEXTS, INJECTION_TOOL_TEXTS, SHORT_BENIGN_OUTPUTS, SHORT_INJECTION_OUTPUTS

BASE_MODEL = "distilbert-base-uncased"
OUTPUT_DIR = "./model"
SEED = 42


def build_augmented_dataset():
    texts = BENIGN_TOOL_TEXTS + INJECTION_TOOL_TEXTS + SHORT_BENIGN_OUTPUTS + SHORT_INJECTION_OUTPUTS
    labels = (
        [0] * len(BENIGN_TOOL_TEXTS) + [1] * len(INJECTION_TOOL_TEXTS)
        + [0] * len(SHORT_BENIGN_OUTPUTS) + [1] * len(SHORT_INJECTION_OUTPUTS)
    )
    train_texts, test_texts, train_labels, test_labels = train_test_split(
        texts, labels, test_size=0.3, random_state=SEED, stratify=labels
    )
    return (
        Dataset.from_dict({"text": train_texts, "label": train_labels}),
        Dataset.from_dict({"text": test_texts, "label": test_labels}),
    )


def metrics_for(logits_and_labels):
    logits, labels = logits_and_labels
    preds = np.argmax(logits, axis=-1)
    precision, recall, f1, _ = precision_recall_fscore_support(labels, preds, average="binary", zero_division=0)
    return {
        "accuracy": accuracy_score(labels, preds),
        "precision": precision,
        "recall": recall,
        "f1": f1,
    }


def main():
    deepset = load_dataset("deepset/prompt-injections")
    aug_train, aug_test = build_augmented_dataset()

    combined_train = concatenate_datasets([deepset["train"], aug_train]).shuffle(seed=SEED)
    combined_test = concatenate_datasets([deepset["test"], aug_test])

    dataset = DatasetDict({"train": combined_train, "test": combined_test})

    tokenizer = AutoTokenizer.from_pretrained(BASE_MODEL)

    def tokenize(batch):
        return tokenizer(batch["text"], truncation=True, padding="max_length", max_length=128)

    tokenized = dataset.map(tokenize, batched=True)
    tokenized = tokenized.rename_column("label", "labels")
    tokenized.set_format(type="torch", columns=["input_ids", "attention_mask", "labels"])

    model = AutoModelForSequenceClassification.from_pretrained(
        BASE_MODEL, num_labels=2, id2label={0: "benign", 1: "injection"}, label2id={"benign": 0, "injection": 1}
    )

    args = TrainingArguments(
        output_dir="./train-output",
        num_train_epochs=5,
        per_device_train_batch_size=16,
        per_device_eval_batch_size=32,
        evaluation_strategy="epoch",
        save_strategy="no",
        logging_strategy="epoch",
        learning_rate=2e-5,
        weight_decay=0.01,
        report_to=[],
    )

    trainer = Trainer(
        model=model,
        args=args,
        train_dataset=tokenized["train"],
        eval_dataset=tokenized["test"],
        compute_metrics=metrics_for,
    )

    trainer.train()

    print("\n=== Final metrics by slice ===")
    combined_metrics = trainer.evaluate()
    print("Combined held-out set:", combined_metrics)

    deepset_test_tok = tokenized["test"].select(range(len(deepset["test"])))
    aug_test_tok = tokenized["test"].select(range(len(deepset["test"]), len(tokenized["test"])))

    deepset_metrics = trainer.evaluate(eval_dataset=deepset_test_tok)
    print("deepset/prompt-injections test slice only:", deepset_metrics)

    aug_metrics = trainer.evaluate(eval_dataset=aug_test_tok)
    print("Tool-description-domain test slice only:", aug_metrics)

    trainer.save_model(OUTPUT_DIR)
    tokenizer.save_pretrained(OUTPUT_DIR)
    print(f"\nSaved fine-tuned model to {OUTPUT_DIR}")


if __name__ == "__main__":
    main()
