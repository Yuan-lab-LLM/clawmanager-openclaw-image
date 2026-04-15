FROM lscr.io/linuxserver/webtop:ubuntu-kde

ENV PUID=1000
ENV PGID=1000
ENV TZ=Asia/Shanghai

LABEL shm_size="1gb"

EXPOSE 3000 3001
