#!/usr/bin/env python3
import argparse
import os
import sys
import tempfile

def main():
    try:
        from transformers import AutoProcessor, DiaForConditionalGeneration
        import torch
    except Exception as exc:
        print(f"error: missing dependencies: {exc}", file=sys.stderr)
        return 1

    parser = argparse.ArgumentParser(description="Dia TTS runner")
    parser.add_argument("--text", required=True, help="Text to synthesize")
    parser.add_argument("--model_id", default="nari-labs/Dia-1.6B-0626", help="Hugging Face model id")
    parser.add_argument("--out_path", default="/tmp/tui-board-tts.wav", help="Output WAV file path")
    parser.add_argument("--pipe_out", action="store_true", help="Write WAV bytes to stdout")
    parser.add_argument("--device", default="cuda", help="cuda or cpu")
    parser.add_argument("--max_new_tokens", type=int, default=1024)
    parser.add_argument("--guidance_scale", type=float, default=3.0)
    parser.add_argument("--temperature", type=float, default=1.1)
    parser.add_argument("--top_p", type=float, default=0.9)
    parser.add_argument("--top_k", type=int, default=45)
    args = parser.parse_args()

    text = " ".join(args.text.strip().split())
    if not text:
        print("error: empty text", file=sys.stderr)
        return 1
    if "[S1]" not in text and "[S2]" not in text:
        text = "[S1] " + text

    device = args.device
    if device == "cuda" and not torch.cuda.is_available():
        device = "cpu"

    processor = AutoProcessor.from_pretrained(args.model_id)
    inputs = processor(text=[text], padding=True, return_tensors="pt").to(device)

    model = DiaForConditionalGeneration.from_pretrained(args.model_id).to(device)
    outputs = model.generate(
        **inputs,
        max_new_tokens=args.max_new_tokens,
        guidance_scale=args.guidance_scale,
        temperature=args.temperature,
        top_p=args.top_p,
        top_k=args.top_k,
    )
    decoded = processor.batch_decode(outputs)

    out_path = args.out_path
    if not out_path.endswith(".wav"):
        out_path = out_path + ".wav"

    processor.save_audio(decoded, out_path)

    if args.pipe_out:
        with open(out_path, "rb") as f:
            sys.stdout.buffer.write(f.read())

    return 0

if __name__ == "__main__":
    sys.exit(main())
