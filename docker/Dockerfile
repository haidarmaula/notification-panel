FROM mcr.microsoft.com/mssql-tools

RUN curl -L \
https://github.com/golang-migrate/migrate/releases/download/v4.18.3/migrate.linux-amd64.tar.gz \
| tar xvz

RUN mv migrate /usr/local/bin/migrate

COPY entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
