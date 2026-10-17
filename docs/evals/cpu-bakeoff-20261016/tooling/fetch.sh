#!/bin/bash
# fetch.sh <repo> <tag> <outfile> : download a GGUF model artifact from Docker Hub ai/ namespace
set -euo pipefail
repo=$1 tag=$2 out=$3
T=$(curl -sS "https://auth.docker.io/token?service=registry.docker.io&scope=repository:ai/$repo:pull" | python3 -c "import json,sys;print(json.load(sys.stdin)['token'])")
# Anonymous Docker Hub manifest GETs are rate limited (HEAD is free): wait for quota.
for i in $(seq 1 90); do
  left=$(curl -sSI -H "Authorization: Bearer $T" -H "Accept: application/vnd.oci.image.manifest.v1+json" "https://registry-1.docker.io/v2/ai/$repo/manifests/$tag" | tr -d '
' | awk -F'[ ;]' 'tolower($1)=="ratelimit-remaining:"{print $2}')
  [ "${left:-1}" -gt 0 ] && break
  [ $i = 1 ] && echo "rate limited; waiting for Docker Hub quota..."
  sleep 120
  T=$(curl -sS "https://auth.docker.io/token?service=registry.docker.io&scope=repository:ai/$repo:pull" | python3 -c "import json,sys;print(json.load(sys.stdin)['token'])")
done
for i in $(seq 1 30); do
  M=$(curl -sS -H "Authorization: Bearer $T" -H "Accept: application/vnd.oci.image.manifest.v1+json" "https://registry-1.docker.io/v2/ai/$repo/manifests/$tag")
  D=$(echo "$M" | python3 -c "
import json,sys
m=json.load(sys.stdin)
l=[x for x in m.get('layers',[]) if (x['mediaType']=='application/vnd.docker.ai.gguf.v3' or x.get('annotations',{}).get('org.cncf.model.filepath','').endswith('.gguf')) and 'mmproj' not in x.get('annotations',{}).get('org.cncf.model.filepath','')]
print(l[0]['digest'] if len(l)==1 else '')") || D=
  [ -n "$D" ] && break
  echo "manifest not available yet ($(echo "$M" | head -c 120)); retrying"; sleep 120
  T=$(curl -sS "https://auth.docker.io/token?service=registry.docker.io&scope=repository:ai/$repo:pull" | python3 -c "import json,sys;print(json.load(sys.stdin)['token'])")
done
[ -n "$D" ] || { echo "no single GGUF layer for $repo:$tag"; exit 1; }
curl -sSL --retry 4 -C - -H "Authorization: Bearer $T" -o "$out" "https://registry-1.docker.io/v2/ai/$repo/blobs/$D"
echo "$D" > "$out.digest"
echo "fetched $repo:$tag -> $out ($(du -h "$out"|cut -f1)) $D"
