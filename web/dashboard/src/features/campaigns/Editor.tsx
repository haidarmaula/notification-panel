import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeftIcon, CheckIcon, PlusIcon, XMarkIcon } from '@heroicons/react/24/outline';
import { Button } from '@astryxdesign/core/Button';
import { Card } from '@astryxdesign/core/Card';
import { CheckboxInput } from '@astryxdesign/core/CheckboxInput';
import { Collapsible } from '@astryxdesign/core/Collapsible';
import { FormLayout } from '@astryxdesign/core/FormLayout';
import { Grid } from '@astryxdesign/core/Grid';
import { Heading } from '@astryxdesign/core/Heading';
import { Icon } from '@astryxdesign/core/Icon';
import { Link } from '@astryxdesign/core/Link';
import { Layout, LayoutContent, LayoutFooter, LayoutHeader } from '@astryxdesign/core/Layout';
import { Section } from '@astryxdesign/core/Section';
import { HStack, VStack } from '@astryxdesign/core/Stack';
import { Text } from '@astryxdesign/core/Text';
import { TextArea } from '@astryxdesign/core/TextArea';
import { TextInput } from '@astryxdesign/core/TextInput';
import { useUnsaved } from '../../app/unsaved';
import { useAuth } from '../../app/auth-context';
import { ApiError, data, errorMessage, request } from '../../lib/api/client';
import { Failure, Loading, RecipientLabel } from '../../components/ui/Feedback';
import type { Application, Campaign, Page } from '../../lib/api/types';
import { draftSchema } from './validation';
import { AndroidNotificationPreview } from './AndroidNotificationPreview';

type Target = { type: 'BROADCAST' } | { type: 'SEGMENT' | 'INDIVIDUAL'; id: string };
type DirectoryItem = {
  id: string;
  name?: string | null;
  external_id?: string;
  description?: string | null;
};

export default function Editor() {
  const { appId = '', campaignId } = useParams();
  const [search] = useSearchParams();
  const auth = useAuth();
  const navigate = useNavigate();
  const client = useQueryClient();
  const setDirty = useUnsaved();
  const editing = Boolean(campaignId);
  const selectedTemplate =
    !campaignId && /^[1-9][0-9]{0,18}$/.test(search.get('template') ?? '')
      ? search.get('template')
      : null;
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [targets, setTargets] = useState<Target[]>([]);
  const [individualTargets, setIndividualTargets] = useState<Target[]>([]);
  const [targetNames, setTargetNames] = useState<Record<string, string>>({});
  const [kind, setKind] = useState<'INDIVIDUAL' | 'SEGMENT'>('INDIVIDUAL');
  const [cursor, setCursor] = useState('');
  const [loaded, setLoaded] = useState('');
  const [templateLoaded, setTemplateLoaded] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<
    Partial<Record<'title' | 'body' | 'targets', string>>
  >({});
  const [previewOpen, setPreviewOpen] = useState(
    () => window.matchMedia('(min-width: 761px)').matches,
  );
  const [error, setError] = useState('');
  const [uncertain, setUncertain] = useState(false);
  const [savedTitle, setSavedTitle] = useState<string | null>(null);
  const titleRef = useRef<HTMLInputElement>(null);
  const bodyRef = useRef<HTMLTextAreaElement>(null);
  const targetsRef = useRef<HTMLInputElement>(null);
  const alive = useRef(true);
  const currentIdentity = useRef({ appId, campaignId });
  currentIdentity.current = { appId, campaignId };
  const template = useQuery({
    queryKey: ['template', appId, selectedTemplate],
    queryFn: ({ signal }) =>
      data<{
        id: string;
        name: string;
        title_template: string;
        body_template: string;
        is_active: boolean;
      }>(`/apps/${appId}/templates/${selectedTemplate}`, signal),
    enabled: Boolean(selectedTemplate),
  });
  const application = useQuery({
    queryKey: ['application', appId],
    queryFn: ({ signal }) => data<Application>(`/apps/${appId}`, signal),
    enabled: Boolean(appId),
  });
  const draft = useQuery({
    queryKey: ['campaign', appId, campaignId],
    queryFn: ({ signal }) => data<Campaign>(`/apps/${appId}/campaigns/${campaignId}`, signal),
    enabled: editing,
  });
  const choices = useQuery({
    queryKey: ['audience', appId, kind, cursor],
    queryFn: ({ signal }) =>
      data<Page<DirectoryItem>>(
        `/apps/${appId}/${kind === 'INDIVIDUAL' ? 'users' : 'segments'}?limit=20${cursor ? `&after_id=${encodeURIComponent(cursor)}` : ''}`,
        signal,
      ),
  });
  const templateData = template.isSuccess ? template.data : undefined;
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    const media = window.matchMedia('(min-width: 761px)');
    const update = () => setPreviewOpen(media.matches);
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);
  useEffect(() => {
    if (templateData?.is_active && !templateLoaded) {
      setTemplateLoaded(true);
      setTitle(templateData.title_template);
      setBody(templateData.body_template);
      setDirty(true);
    }
  }, [templateData, templateLoaded, setDirty]);
  useEffect(() => {
    const campaign = draft.data;
    const identity = `${appId}:${campaignId}`;
    if (campaign && loaded !== identity) {
      setTitle(campaign.title);
      setBody(campaign.body);
      setTargets(
        campaign.targets.flatMap<Target>((target) =>
          target.type === 'BROADCAST'
            ? [{ type: 'BROADCAST' }]
            : (target.type === 'INDIVIDUAL' || target.type === 'SEGMENT') && target.id
              ? [{ type: target.type, id: target.id }]
              : [],
        ),
      );
      setLoaded(identity);
    }
  }, [draft.data, loaded, appId, campaignId]);

  const mutation = useMutation({
    mutationFn: async (payload: {
      title: string;
      body: string;
      targets: Target[];
      template_id: string | null;
    }) =>
      request<{ data: { id: string } }>(
        `/bff/api/apps/${appId}/campaigns${campaignId ? `/${campaignId}` : ''}`,
        {
          method: editing ? 'PUT' : 'POST',
          csrf: auth.csrf(),
          body: payload,
        },
      ),
  });
  const templateDisabled = Boolean(
    selectedTemplate && (!templateLoaded || !templateData?.is_active),
  );
  const editable =
    !editing ||
    (draft.data?.status === 'DRAFT' &&
      !draft.data.targets.some((target) => target.type === 'UPLOAD'));
  const isBroadcast = targets.some((target) => target.type === 'BROADCAST');
  const titleBytes = useMemo(() => new TextEncoder().encode(title).length, [title]);
  const bodyBytes = useMemo(() => new TextEncoder().encode(body).length, [body]);

  function add(item: DirectoryItem) {
    const selected: Target = { type: kind, id: item.id };
    if (
      !targets.some(
        (target) => target.type === selected.type && 'id' in target && target.id === item.id,
      )
    ) {
      setTargets((current) => [
        ...current.filter((target) => target.type !== 'BROADCAST'),
        selected,
      ]);
      setTargetNames((current) => ({
        ...current,
        [`${kind}:${item.id}`]: item.name || item.external_id || '',
      }));
      setFieldErrors((current) => ({ ...current, targets: undefined }));
      setDirty(true);
    }
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (mutation.isPending || uncertain) return;
    setError('');
    setFieldErrors({});
    const parsed = draftSchema.safeParse({ title, body, targets });
    if (!parsed.success) {
      const errors = parsed.error.flatten().fieldErrors;
      setFieldErrors({
        title: errors.title?.[0],
        body: errors.body?.[0],
        targets: errors.targets?.[0],
      });
      const invalid = errors.title
        ? titleRef.current
        : errors.body
          ? bodyRef.current
          : targetsRef.current;
      invalid?.focus();
      invalid?.scrollIntoView({ block: 'center' });
      return;
    }
    const currentAppId = appId;
    const currentCampaignId = campaignId;
    try {
      const result = await mutation.mutateAsync({
        ...parsed.data,
        template_id: draft.data?.template_id ?? (templateLoaded ? selectedTemplate : null),
      });
      if (
        alive.current &&
        currentIdentity.current.appId === currentAppId &&
        currentIdentity.current.campaignId === currentCampaignId
      )
        setDirty(false);
      await client.invalidateQueries({ queryKey: ['campaigns', currentAppId] });
      await client.invalidateQueries({ queryKey: ['campaign', currentAppId, result.data.id] });
      if (
        alive.current &&
        currentIdentity.current.appId === currentAppId &&
        currentIdentity.current.campaignId === currentCampaignId
      )
        navigate(`/apps/${currentAppId}/campaigns/${result.data.id}`);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401)
        window.dispatchEvent(new Event('hello:session-expired'));
      if (cause instanceof ApiError && (cause.status === 0 || cause.status >= 500)) {
        setUncertain(true);
        setSavedTitle(title);
      }
      setError(errorMessage(cause));
    }
  }

  return (
    <Layout
      height="fill"
      padding={6}
      contentWidth={1120}
      header={
        <LayoutHeader>
          <VStack gap={3}>
            <Link href={`/apps/${appId}/campaigns`}>
              <HStack gap={2} vAlign="center">
                <Icon icon={ArrowLeftIcon} size="sm" />
                <Text>All campaigns</Text>
              </HStack>
            </Link>
            <VStack gap={1}>
              <Text>{editing ? `DRAFT #${campaignId}` : 'NEW CAMPAIGN'}</Text>
              <Heading level={1}>{editing ? 'Edit draft' : 'Create campaign'}</Heading>
              <Text color="secondary">Save as a draft. Sending happens in a separate step.</Text>
            </VStack>
          </VStack>
        </LayoutHeader>
      }
      footer={
        editable && !(editing && draft.isPending) ? (
          <LayoutFooter hasDivider>
            <HStack gap={2} wrap="wrap" hAlign="start">
              <Button
                label="Back"
                variant="ghost"
                icon={<Icon icon={ArrowLeftIcon} size="sm" />}
                href={`/apps/${appId}/campaigns`}
              />
              <Button
                label={mutation.isPending ? 'Saving…' : 'Save draft'}
                variant="primary"
                type="submit"
                form="campaign-form"
                icon={<Icon icon={CheckIcon} size="sm" />}
                isDisabled={mutation.isPending || uncertain || templateDisabled}
              />
            </HStack>
          </LayoutFooter>
        ) : undefined
      }
    >
      <LayoutContent>
        <VStack gap={4}>
          {selectedTemplate && template.isError && (
            <Failure error={template.error} retry={() => void template.refetch()} />
          )}
          {selectedTemplate && templateData && !templateData.is_active && (
            <Text role="alert">
              This template is inactive. Choose another template or create a draft without one.
            </Text>
          )}
          {selectedTemplate && templateData?.is_active && (
            <Text>
              From template: {templateData.name}. Review and adjust the message before saving.
            </Text>
          )}
          {editing && draft.isPending ? (
            <Loading variant="form" />
          ) : editing && draft.isError ? (
            <Failure error={draft.error} retry={() => void draft.refetch()} />
          ) : !editable ? (
            <Text>
              This campaign cannot be edited with this form. Only drafts with supported targets can
              be changed.
            </Text>
          ) : (
            <form id="campaign-form" noValidate onSubmit={(event) => void save(event)}>
              <Grid columns={{ minWidth: 420, max: 2 }} columnGap={8} rowGap={6} align="start">
                <VStack gap={6}>
                  <Section padding={6}>
                    <VStack gap={4}>
                      <Heading level={2}>Message</Heading>
                      <Text color="secondary">Write a concise title and a clear message.</Text>
                      <FormLayout>
                        <TextInput
                          ref={titleRef}
                          label="Notification title"
                          value={title}
                          onChange={(value) => {
                            setTitle(value);
                            setFieldErrors((current) => ({ ...current, title: undefined }));
                            setDirty(true);
                          }}
                          description={`${titleBytes}/255 bytes${fieldErrors.title ? ` · ${fieldErrors.title}` : ''}`}
                          status={
                            fieldErrors.title
                              ? { type: 'error', message: fieldErrors.title }
                              : undefined
                          }
                          statusVariant="tooltip"
                          placeholder="What would you like to say?"
                          isRequired
                          isDisabled={mutation.isPending || uncertain || templateDisabled}
                        />
                        <TextArea
                          ref={bodyRef}
                          label="Message"
                          value={body}
                          onChange={(value) => {
                            setBody(value);
                            setFieldErrors((current) => ({ ...current, body: undefined }));
                            setDirty(true);
                          }}
                          description={`${bodyBytes}/4096 bytes${fieldErrors.body ? ` · ${fieldErrors.body}` : ''}`}
                          status={
                            fieldErrors.body
                              ? { type: 'error', message: fieldErrors.body }
                              : undefined
                          }
                          statusVariant="tooltip"
                          rows={5}
                          placeholder="Write a message for your recipients…"
                          isRequired
                          isDisabled={mutation.isPending || uncertain || templateDisabled}
                        />
                      </FormLayout>
                    </VStack>
                  </Section>
                  <Section padding={6}>
                    <VStack gap={4}>
                      <Heading level={2}>Recipients</Heading>
                      <Text color="secondary">Choose users or segments in this application.</Text>
                      <CheckboxInput
                        ref={targetsRef}
                        label="All recipients in this application"
                        value={isBroadcast}
                        onChange={(checked) => {
                          if (checked) {
                            setIndividualTargets(targets);
                            setTargets([{ type: 'BROADCAST' }]);
                          } else setTargets(individualTargets);
                          setFieldErrors((current) => ({ ...current, targets: undefined }));
                          setDirty(true);
                        }}
                        isDisabled={mutation.isPending || uncertain || templateDisabled}
                        status={
                          fieldErrors.targets
                            ? { type: 'error', message: fieldErrors.targets }
                            : undefined
                        }
                      />
                      {!isBroadcast && (
                        <>
                          <HStack gap={2}>
                            <Button
                              label="Users"
                              variant={kind === 'INDIVIDUAL' ? 'primary' : 'secondary'}
                              onClick={() => {
                                setKind('INDIVIDUAL');
                                setCursor('');
                              }}
                            />
                            <Button
                              label="Segments"
                              variant={kind === 'SEGMENT' ? 'primary' : 'secondary'}
                              onClick={() => {
                                setKind('SEGMENT');
                                setCursor('');
                              }}
                            />
                          </HStack>
                          {choices.isPending ? (
                            <Loading label="Loading recipients…" />
                          ) : choices.isError ? (
                            <Failure error={choices.error} retry={() => void choices.refetch()} />
                          ) : (
                            <VStack gap={2}>
                              {choices.data?.items.map((item) => (
                                <HStack
                                  key={item.id}
                                  gap={3}
                                  wrap="wrap"
                                  hAlign="between"
                                  vAlign="center"
                                >
                                  <Text>
                                    {item.name || item.external_id || `#${item.id}`}{' '}
                                    <Text color="secondary">#{item.id}</Text>
                                  </Text>
                                  <Button
                                    label={`${item.name || item.external_id || `#${item.id}`} #${item.id}`}
                                    variant="secondary"
                                    size="sm"
                                    icon={<Icon icon={PlusIcon} size="sm" />}
                                    isDisabled={
                                      mutation.isPending ||
                                      uncertain ||
                                      targets.length >= 100 ||
                                      targets.some(
                                        (target) =>
                                          target.type === kind &&
                                          'id' in target &&
                                          target.id === item.id,
                                      )
                                    }
                                    onClick={() => add(item)}
                                  />
                                </HStack>
                              ))}
                              {!choices.data?.items.length && (
                                <Text>No recipients on this page.</Text>
                              )}
                              <HStack gap={2}>
                                <Button
                                  label="First page"
                                  variant="secondary"
                                  size="sm"
                                  isDisabled={!cursor}
                                  onClick={() => setCursor('')}
                                />
                                <Button
                                  label="Next page"
                                  variant="secondary"
                                  size="sm"
                                  isDisabled={!choices.data?.next_after_id}
                                  onClick={() => setCursor(choices.data?.next_after_id ?? '')}
                                />
                              </HStack>
                            </VStack>
                          )}
                          <VStack gap={2} role="group" aria-label="Selected recipients">
                            {targets.map((target, index) => (
                              <HStack
                                key={`${target.type}:${'id' in target ? target.id : index}`}
                                gap={2}
                                vAlign="center"
                              >
                                <RecipientLabel
                                  appId={appId}
                                  target={target}
                                  name={
                                    'id' in target
                                      ? targetNames[`${target.type}:${target.id}`]
                                      : undefined
                                  }
                                />
                                <Button
                                  label={`Remove target ${'id' in target ? target.id : ''}`}
                                  variant="ghost"
                                  size="sm"
                                  icon={<Icon icon={XMarkIcon} size="sm" />}
                                  isDisabled={mutation.isPending || uncertain || templateDisabled}
                                  onClick={() => {
                                    setTargets((current) =>
                                      current.filter((_, itemIndex) => itemIndex !== index),
                                    );
                                    setDirty(true);
                                  }}
                                />
                              </HStack>
                            ))}
                          </VStack>
                        </>
                      )}
                    </VStack>
                  </Section>
                  {error && <Text role="alert">{error}</Text>}
                  {uncertain && (
                    <Card>
                      <VStack gap={3}>
                        <Text>
                          The save result is uncertain for “{savedTitle}”. Check the campaign list
                          before creating or saving again.
                        </Text>
                        <Link href={`/apps/${appId}/campaigns`}>Check campaign list</Link>
                      </VStack>
                    </Card>
                  )}
                </VStack>
                <Collapsible
                  trigger="Notification preview"
                  isOpen={previewOpen}
                  onOpenChange={setPreviewOpen}
                >
                  <AndroidNotificationPreview
                    applicationName={application.data?.name ?? 'Application'}
                    title={title}
                    body={body}
                  />
                </Collapsible>
              </Grid>
            </form>
          )}
        </VStack>
      </LayoutContent>
    </Layout>
  );
}
