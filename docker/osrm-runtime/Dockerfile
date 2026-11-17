# Build context: repository root. Match the preparation build and pin its digest.
ARG OSRM_BASE=ghcr.io/project-osrm/osrm-backend:v26.11.0-debian@sha256:e1929ae2818226284d7ef007329098a30851c11528ab3712bc9c8d68da78278b
FROM ${OSRM_BASE}
USER root
# Debian trixie packages AWS CLI v2, avoiding an unverified installer download.
RUN apt-get update && apt-get install -y --no-install-recommends \
    awscli bash coreutils ca-certificates \
    && aws --version 2>&1 | grep '^aws-cli/2\.' \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 osrm-runtime \
    && useradd --uid 10001 --gid 10001 --no-create-home osrm-runtime \
    && install -d -m 0700 -o 10001 -g 10001 /data/osrm
COPY scripts/start-osrm-from-s3.sh scripts/staging-images-common.sh /opt/shopstops/
USER 10001:10001
ENV OSRM_DATA_PARENT=/data/osrm
EXPOSE 5001
ENTRYPOINT ["/bin/bash", "/opt/shopstops/start-osrm-from-s3.sh"]