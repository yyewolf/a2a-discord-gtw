# syntax=docker/dockerfile:1
# Built by goreleaser (dockers_v2). The build context contains
# <os>/<arch>/a2a-discord-gtw for each platform and docker/data/.keep.
FROM scratch
ARG TARGETPLATFORM
COPY --chown=65532:65532 docker/data/ /data/
COPY --chmod=0555 $TARGETPLATFORM/a2a-discord-gtw /a2a-discord-gtw
USER 65532:65532
ENV ADG_DATA_DIR=/data \
    ADG_LISTEN=:8080
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/a2a-discord-gtw", "healthcheck"]
ENTRYPOINT ["/a2a-discord-gtw"]
CMD ["serve"]
