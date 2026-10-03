#!/bin/sh
# Regenerates guardpb/ from proto/guard.proto. Run from this directory with
# the venv active.
set -e

python3 -m grpc_tools.protoc -I proto --python_out=guardpb --grpc_python_out=guardpb proto/guard.proto
touch guardpb/__init__.py

# grpc_tools' codegen emits an absolute `import guard_pb2`, which only works
# if the generated directory is on sys.path directly - it breaks as soon as
# guardpb is imported as a package (`from guardpb import ...`), which is how
# server.py uses it. Rewriting to a relative import is the standard fix.
sed -i '' 's/^import guard_pb2 as guard__pb2$/from . import guard_pb2 as guard__pb2/' guardpb/guard_pb2_grpc.py

echo "Regenerated guardpb/ (with the relative-import fix applied)."
