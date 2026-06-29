#!/bin/bash

set -e

echo "Waiting SQL Server..."

until /opt/mssql-tools/bin/sqlcmd \
    -S sqlserver \
    -U sa \
    -P "$DB_PASSWORD" \
    -C \
    -Q "SELECT 1"
do
    sleep 2
done

echo "SQL Server Ready."

echo "Creating database..."

/opt/mssql-tools/bin/sqlcmd \
    -S sqlserver \
    -U sa \
    -P "$DB_PASSWORD" \
    -C \
    -i /docker/init-db.sql

echo "Running migration..."

migrate \
-path /migrations \
-database "sqlserver://sa:${DB_PASSWORD}@sqlserver:1433?database=${DB_NAME}&encrypt=disable" \
up

echo "Done."
