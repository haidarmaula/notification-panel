import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '@astryxdesign/core/reset.css';
import '@astryxdesign/core/astryx.css';
import { Theme } from '@astryxdesign/core/theme';
import { themes, defaultThemeSlug, type ThemeSlug } from './astryx-themes';
import { AppShell } from '@astryxdesign/core/AppShell';
import { TopNav, TopNavHeading } from '@astryxdesign/core/TopNav';
import { SideNav, SideNavSection, SideNavItem } from '@astryxdesign/core/SideNav';
import { Layout, LayoutHeader, LayoutContent, LayoutFooter } from '@astryxdesign/core/Layout';
import { VStack, HStack, StackItem } from '@astryxdesign/core/Stack';
import { Heading, Text } from '@astryxdesign/core/Text';
import { Button } from '@astryxdesign/core/Button';
import { IconButton } from '@astryxdesign/core/IconButton';
import { Icon } from '@astryxdesign/core/Icon';
import { NavIcon } from '@astryxdesign/core/NavIcon';
import {
  DropdownMenu,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
} from '@astryxdesign/core/DropdownMenu';
import { Avatar } from '@astryxdesign/core/Avatar';
import { TextInput } from '@astryxdesign/core/TextInput';
import { Table, proportional, pixel, type TableColumn } from '@astryxdesign/core/Table';
import { Token, type TokenColor } from '@astryxdesign/core/Token';
import { TabList, Tab } from '@astryxdesign/core/TabList';
import { EmptyState } from '@astryxdesign/core/EmptyState';
import { Dialog, DialogHeader } from '@astryxdesign/core/Dialog';
import {
  BellIcon,
  PaperAirplaneIcon,
  ChartBarIcon,
  QueueListIcon,
  DocumentDuplicateIcon,
  UserGroupIcon,
  UsersIcon,
  Cog6ToothIcon,
  PlusIcon,
  MagnifyingGlassIcon,
  MoonIcon,
  SunIcon,
  ArrowRightIcon,
  CheckCircleIcon,
  ClockIcon,
  PencilSquareIcon,
} from '@heroicons/react/24/outline';

type Campaign = {
  id: string;
  title: string;
  status: 'DRAFT' | 'QUEUED' | 'SENT';
  audience: string;
};
const samples: Campaign[] = [
  {
    id: 'sample-001',
    title: 'Informasi layanan akhir pekan',
    status: 'DRAFT',
    audience: 'Semua anggota',
  },
  {
    id: 'sample-002',
    title: 'Pembaruan aplikasi anggota',
    status: 'QUEUED',
    audience: 'Pengguna aplikasi',
  },
  {
    id: 'sample-003',
    title: 'Pengingat pembayaran November',
    status: 'SENT',
    audience: 'Anggota aktif',
  },
  {
    id: 'sample-004',
    title: 'Jadwal operasional cabang',
    status: 'DRAFT',
    audience: 'Semua anggota',
  },
  {
    id: 'sample-005',
    title: 'Selamat datang, anggota baru',
    status: 'SENT',
    audience: 'Anggota baru',
  },
  {
    id: 'sample-006',
    title: 'Pemeliharaan layanan digital',
    status: 'DRAFT',
    audience: 'Pengguna aplikasi',
  },
];
const statuses: Record<
  Campaign['status'],
  { label: string; color: TokenColor; icon: typeof PencilSquareIcon }
> = {
  DRAFT: { label: 'Draf', color: 'gray', icon: PencilSquareIcon },
  QUEUED: { label: 'Dalam antrean', color: 'blue', icon: ClockIcon },
  SENT: { label: 'Terkirim', color: 'green', icon: CheckCircleIcon },
};
const navigation = [
  { label: 'Kampanye', icon: PaperAirplaneIcon },
  { label: 'Ringkasan', icon: ChartBarIcon },
  { label: 'Log pengiriman', icon: QueueListIcon },
  { label: 'Template', icon: DocumentDuplicateIcon },
  { label: 'Segmen', icon: UserGroupIcon },
  { label: 'Audiens & perangkat', icon: UsersIcon },
];

const themeLabels: Record<ThemeSlug, string> = {
  neutral: 'Neutral',
  y2k: 'Y2K',
  matcha: 'Matcha',
  stone: 'Stone',
  butter: 'Butter',
  chocolate: 'Chocolate',
  gothic: 'Gothic',
};

function Preview() {
  const [themeSlug, setThemeSlug] = useState<ThemeSlug>(defaultThemeSlug);
  const [mode, setMode] = useState<'light' | 'dark'>('light');
  const [page, setPage] = useState('Kampanye');
  const [query, setQuery] = useState('');
  const [scope, setScope] = useState('ALL');
  const [rows, setRows] = useState(samples);
  const [draft, setDraft] = useState(false);
  const [title, setTitle] = useState('');
  const [selected, setSelected] = useState<Campaign | null>(null);
  const [mobileOpen, setMobileOpen] = useState(false);
  const visible = rows.filter(
    (row) =>
      (scope === 'ALL' || row.status === scope) &&
      row.title.toLocaleLowerCase('id').includes(query.toLocaleLowerCase('id')),
  );
  const go = (label: string) => {
    setPage(label);
    setMobileOpen(false);
  };
  const columns: TableColumn<Campaign>[] = [
    {
      key: 'title',
      width: proportional(2),
      header: 'Nama kampanye',
      renderCell: (row) => (
        <VStack gap={1}>
          <Text weight="semibold">{row.title}</Text>
          <Text type="supporting" color="secondary">
            {row.id}
          </Text>
        </VStack>
      ),
    },
    {
      key: 'status',
      width: pixel(180),
      header: 'Status',
      renderCell: (row) => (
        <Token
          label={statuses[row.status].label}
          color={statuses[row.status].color}
          icon={<Icon icon={statuses[row.status].icon} size="xsm" />}
        />
      ),
    },
    {
      key: 'audience',
      width: proportional(1),
      header: 'Audiens',
      renderCell: (row) => <Text color="secondary">{row.audience}</Text>,
    },
    {
      key: 'actions',
      width: pixel(48),
      header: '',
      renderCell: (row) => (
        <IconButton
          label={`Lihat ${row.title}`}
          icon={<Icon icon={ArrowRightIcon} size="sm" />}
          variant="ghost"
          onClick={() => setSelected(row)}
        />
      ),
    },
  ];
  return (
    <Theme theme={themes[themeSlug]} mode={mode}>
      <AppShell
        contentPadding={0}
        mobileNav={{ isOpen: mobileOpen, onOpenChange: setMobileOpen }}
        topNav={
          <TopNav
            heading={
              <TopNavHeading
                heading="Hello"
                logo={<NavIcon icon={<Icon icon={BellIcon} size="sm" />} />}
              />
            }
            startContent={<Text color="secondary">Notification workspace</Text>}
            endContent={
              <HStack gap={3} vAlign="center" hAlign="end">
                <DropdownMenu
                  button={{
                    label: `Tema: ${themeLabels[themeSlug]}`,
                    size: 'sm',
                    variant: 'ghost',
                  }}
                  alignment="end"
                >
                  <DropdownMenuRadioGroup
                    label="Tema preview"
                    value={themeSlug}
                    onChange={(value) => {
                      if (
                        value === 'neutral' ||
                        value === 'y2k' ||
                        value === 'matcha' ||
                        value === 'stone' ||
                        value === 'butter' ||
                        value === 'chocolate' ||
                        value === 'gothic'
                      )
                        setThemeSlug(value);
                    }}
                  >
                    {(Object.keys(themeLabels) as ThemeSlug[]).map((slug) => (
                      <DropdownMenuRadioItem key={slug} value={slug} label={themeLabels[slug]} />
                    ))}
                  </DropdownMenuRadioGroup>
                </DropdownMenu>
                <IconButton
                  label={mode === 'light' ? 'Mode gelap' : 'Mode terang'}
                  icon={<Icon icon={mode === 'light' ? MoonIcon : SunIcon} size="sm" />}
                  variant="ghost"
                  onClick={() => setMode(mode === 'light' ? 'dark' : 'light')}
                />
                <Avatar name="Admin Demo" size="sm" tooltip="Akun contoh" />
              </HStack>
            }
          />
        }
        sideNav={
          <SideNav
            header={
              <VStack padding={3} gap={1}>
                <Text weight="semibold">Aplikasi anggota</Text>
                <Text type="supporting" color="secondary">
                  Workspace contoh
                </Text>
              </VStack>
            }
            footer={
              <SideNavSection title="Akun" isHeaderHidden>
                <SideNavItem
                  label="Pengaturan"
                  icon={Cog6ToothIcon}
                  isSelected={page === 'Pengaturan'}
                  onClick={() => go('Pengaturan')}
                />
              </SideNavSection>
            }
          >
            <SideNavSection title="Workspace" isHeaderHidden>
              {navigation.slice(0, 3).map((item) => (
                <SideNavItem
                  key={item.label}
                  {...item}
                  isSelected={page === item.label}
                  onClick={() => go(item.label)}
                />
              ))}
            </SideNavSection>
            <SideNavSection title="Kelola">
              {navigation.slice(3).map((item) => (
                <SideNavItem
                  key={item.label}
                  {...item}
                  isSelected={page === item.label}
                  onClick={() => go(item.label)}
                />
              ))}
            </SideNavSection>
          </SideNav>
        }
      >
        <Layout
          padding={6}
          contentWidth={1120}
          header={
            <LayoutHeader>
              <VStack gap={5}>
                <HStack gap={3} wrap="wrap" vAlign="center">
                  <StackItem size="fill">
                    <VStack gap={2}>
                      <Heading level={1}>{page}</Heading>
                      <Text color="secondary">
                        {page === 'Kampanye'
                          ? 'Kelola notifikasi, dari draf hingga pengiriman.'
                          : 'Workspace notifikasi anggota.'}
                      </Text>
                    </VStack>
                  </StackItem>
                  {page === 'Kampanye' && (
                    <Button
                      label="Buat kampanye"
                      variant="primary"
                      icon={<Icon icon={PlusIcon} size="sm" />}
                      onClick={() => setDraft(true)}
                    />
                  )}
                </HStack>
                {page === 'Kampanye' && (
                  <TabList value={scope} onChange={setScope}>
                    <Tab value="ALL" label="Semua kampanye" />
                    <Tab value="DRAFT" label="Draf" />
                    <Tab value="QUEUED" label="Dalam antrean" />
                    <Tab value="SENT" label="Terkirim" />
                  </TabList>
                )}
              </VStack>
            </LayoutHeader>
          }
          content={
            <LayoutContent>
              {page === 'Kampanye' ? (
                <VStack gap={5}>
                  <HStack gap={3} wrap="wrap" vAlign="center">
                    <StackItem size="fill">
                      <Text color="secondary">{visible.length} kampanye</Text>
                    </StackItem>
                    <VStack width={320}>
                      <TextInput
                        label="Cari kampanye"
                        isLabelHidden
                        placeholder="Cari kampanye…"
                        startIcon={MagnifyingGlassIcon}
                        value={query}
                        onChange={setQuery}
                        hasClear
                      />
                    </VStack>
                  </HStack>
                  {visible.length ? (
                    <Table
                      data={visible}
                      columns={columns}
                      idKey="id"
                      density="balanced"
                      hasHover
                    />
                  ) : (
                    <EmptyState
                      title="Tidak ada kampanye yang cocok"
                      description="Coba kata kunci atau status lain."
                      icon={<Icon icon={MagnifyingGlassIcon} size="lg" />}
                      actions={
                        <Button
                          label="Reset filter"
                          onClick={() => {
                            setQuery('');
                            setScope('ALL');
                          }}
                        />
                      }
                    />
                  )}
                </VStack>
              ) : page === 'Pengaturan' ? (
                <VStack gap={4}>
                  <Heading level={2}>Tampilan</Heading>
                  <Text>Tema {themeLabels[themeSlug]} dengan komponen bawaan Astryx.</Text>
                  <Button
                    label={mode === 'light' ? 'Gunakan mode gelap' : 'Gunakan mode terang'}
                    onClick={() => setMode(mode === 'light' ? 'dark' : 'light')}
                  />
                </VStack>
              ) : (
                <EmptyState
                  title={`Pratinjau ${page.toLocaleLowerCase('id')}`}
                  description="Halaman ini belum dimigrasikan. Tinjau desain utama melalui halaman kampanye."
                  icon={
                    <Icon icon={navigation.find((item) => item.label === page)!.icon} size="lg" />
                  }
                  actions={<Button label="Kembali ke kampanye" onClick={() => go('Kampanye')} />}
                />
              )}
            </LayoutContent>
          }
          footer={
            <LayoutFooter hasDivider>
              <Text type="supporting" color="secondary">
                Data contoh · Tidak terhubung ke layanan pengiriman
              </Text>
            </LayoutFooter>
          }
        />
      </AppShell>
      <Dialog isOpen={draft} onOpenChange={setDraft} aria-label="Buat kampanye contoh">
        <DialogHeader title="Buat kampanye contoh" onOpenChange={setDraft} />
        <VStack gap={5} padding={5}>
          <Text>Draf ini hanya disimpan selama pratinjau dibuka.</Text>
          <TextInput label="Judul kampanye" value={title} onChange={setTitle} hasAutoFocus />
          <HStack gap={2} hAlign="end">
            <Button label="Batal" onClick={() => setDraft(false)} />
            <Button
              label="Simpan draf"
              variant="primary"
              isDisabled={!title.trim()}
              onClick={() => {
                setRows([
                  {
                    id: `sample-${rows.length + 1}`,
                    title: title.trim(),
                    status: 'DRAFT',
                    audience: 'Belum dipilih',
                  },
                  ...rows,
                ]);
                setTitle('');
                setDraft(false);
                setScope('ALL');
                setQuery('');
              }}
            />
          </HStack>
        </VStack>
      </Dialog>
      <Dialog
        isOpen={!!selected}
        onOpenChange={(open) => {
          if (!open) setSelected(null);
        }}
        aria-label="Detail kampanye contoh"
      >
        <DialogHeader
          title="Detail kampanye contoh"
          onOpenChange={(open) => {
            if (!open) setSelected(null);
          }}
        />
        {selected && (
          <VStack gap={4} padding={5}>
            <Heading level={2}>{selected.title}</Heading>
            <Token
              label={statuses[selected.status].label}
              color={statuses[selected.status].color}
            />
            <Text>ID: {selected.id}</Text>
            <Text>Audiens: {selected.audience}</Text>
            <Text color="secondary">Data contoh. Tidak ada notifikasi yang dikirim.</Text>
            <Button label="Tutup" onClick={() => setSelected(null)} />
          </VStack>
        )}
      </Dialog>
    </Theme>
  );
}
createRoot(document.getElementById('root')!).render(<Preview />);
