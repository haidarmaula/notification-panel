import { Text } from '@astryxdesign/core/Text';
import { Link } from '@astryxdesign/core/Link';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Token } from '@astryxdesign/core/Token';
import { Section } from '@astryxdesign/core/Section';
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableHeaderCell,
  TableRow,
} from '@astryxdesign/core/Table';
import { useParams } from 'react-router-dom';
import type { components } from '../../lib/api/generated/staff';
import { date, usePage } from './data-hooks';
import { Pager, QueryState } from './shared';
import { PageFrame } from '../../components/ui/PageFrame';
type Upload = components['schemas']['Upload'];
type UploadRow = components['schemas']['UploadRow'];
type Audit = components['schemas']['Audit'];

function enumLabel(value: string) {
  const label = value.replaceAll('_', ' ').toLowerCase();
  return label.charAt(0).toUpperCase() + label.slice(1);
}

export default function Uploads() {
  const { appId = '' } = useParams();
  const page = usePage<Upload>(`/apps/${appId}/uploads`);
  return (
    <PageFrame
      title="Upload history"
      description="Review batches recorded through the application integration."
    >
      <QueryState query={page.query}>
        {page.query.data?.items.length ? (
          <Section padding={6}>
            <Table density="balanced" hasHover aria-label="Upload history">
              <TableHeader>
                <TableRow isHeaderRow>
                  <TableHeaderCell>Batch</TableHeaderCell>
                  <TableHeaderCell>Total rows</TableHeaderCell>
                  <TableHeaderCell>Valid</TableHeaderCell>
                  <TableHeaderCell>Invalid</TableHeaderCell>
                  <TableHeaderCell>Recorded</TableHeaderCell>
                </TableRow>
              </TableHeader>
              <TableBody>
                {page.query.data.items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell>
                      <Link href={`/apps/${appId}/uploads/${item.id}`}>Batch #{item.id}</Link>
                    </TableCell>
                    <TableCell>{item.total_rows}</TableCell>
                    <TableCell>{item.valid_rows}</TableCell>
                    <TableCell>{item.invalid_rows}</TableCell>
                    <TableCell>{date(item.created_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Section>
        ) : (
          <EmptyState
            title="No upload history"
            description="Upload batches will appear here after they are recorded through the application integration."
          />
        )}
        <Pager page={page} />
      </QueryState>
    </PageFrame>
  );
}
export function UploadRows() {
  const { appId = '', batchId = '' } = useParams();
  const page = usePage<UploadRow>(`/apps/${appId}/uploads/${batchId}/rows`);
  return (
    <PageFrame
      title={`Batch #${batchId}`}
      description="Validation status recorded by the backend."
      back={{ label: 'Back to upload history', href: `/apps/${appId}/uploads` }}
    >
      <QueryState query={page.query}>
        {page.query.data?.items.length ? (
          <Section padding={6}>
            <Table density="balanced" hasHover aria-label={`Rows in batch ${batchId}`}>
              <TableHeader>
                <TableRow isHeaderRow>
                  <TableHeaderCell>Row ID</TableHeaderCell>
                  <TableHeaderCell>Validation</TableHeaderCell>
                  <TableHeaderCell>Recorded</TableHeaderCell>
                </TableRow>
              </TableHeader>
              <TableBody>
                {page.query.data.items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell>#{item.id}</TableCell>
                    <TableCell>
                      <Token
                        label={item.is_valid ? 'Valid' : 'Invalid'}
                        color={item.is_valid ? 'green' : 'red'}
                        size="sm"
                      />
                    </TableCell>
                    <TableCell>{date(item.created_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Section>
        ) : (
          <EmptyState
            title="No rows recorded"
            description="This batch has no recorded rows."
            isCompact
          />
        )}
        <Pager page={page} />
      </QueryState>
      <Text>The API does not provide row contents or rejection reasons.</Text>
    </PageFrame>
  );
}
export function AuditLog() {
  const { appId = '' } = useParams();
  const page = usePage<Audit>(`/apps/${appId}/audit-logs`);
  return (
    <PageFrame
      title="Activity audit"
      description="Actions recorded for this application, ordered by record ID."
    >
      <QueryState query={page.query}>
        {page.query.data?.items.length ? (
          <Section padding={6}>
            <Table density="balanced" hasHover aria-label="Application audit activity">
              <TableHeader>
                <TableRow isHeaderRow>
                  <TableHeaderCell>Activity</TableHeaderCell>
                  <TableHeaderCell>Actor</TableHeaderCell>
                  <TableHeaderCell>Object</TableHeaderCell>
                  <TableHeaderCell>Recorded</TableHeaderCell>
                </TableRow>
              </TableHeader>
              <TableBody>
                {page.query.data.items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell>
                      <Text display="block">{enumLabel(item.action)}</Text>
                      <Text display="block" type="supporting" color="secondary">
                        {item.action} · #{item.id}
                      </Text>
                    </TableCell>
                    <TableCell>Staff #{item.actor_id}</TableCell>
                    <TableCell>
                      <Text display="block">{enumLabel(item.entity_type)}</Text>
                      <Text
                        display="block"
                        type="supporting"
                        color="secondary"
                        wordBreak="break-all"
                      >
                        {item.entity_type}
                        {item.entity_id && ` · #${item.entity_id}`}
                      </Text>
                    </TableCell>
                    <TableCell>{date(item.created_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Section>
        ) : (
          <EmptyState
            title="No activity recorded"
            description="Application actions will appear here when the backend records them."
          />
        )}
        <Pager page={page} />
      </QueryState>
    </PageFrame>
  );
}
