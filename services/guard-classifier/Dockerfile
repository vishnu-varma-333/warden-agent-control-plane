# The classifier's trained weights (model/) are gitignored (~512MB, well
# over GitHub's file-size limits — see this directory's README) so they
# are NOT fetched by `git clone` and therefore not in a fresh Docker build
# context either unless you put them there yourself first. This image
# expects ./model to already exist in the build context when you run
# `docker build` — e.g. copied or scp'd from wherever it was trained. A
# real deployment would fetch it from object storage (S3) at build or
# startup time instead; tracked as a follow-up, not hidden — see
# DECISIONS.md and docs/MILESTONES.md.
FROM python:3.12-slim
WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY guardpb/ guardpb/
COPY server.py .
COPY model/ model/

EXPOSE 50051 8000
CMD ["python3", "server.py"]
