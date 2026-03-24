#!/bin/sh
while true; do
  curl -s http://localhost:8000/v1/completions \
    -H "Content-Type: application/json" \
    -d '{"model":"Qwen/Qwen2.5-1.5B-Instruct","prompt":"Explain GPU computing","max_tokens":500}' \
    > /dev/null
done
