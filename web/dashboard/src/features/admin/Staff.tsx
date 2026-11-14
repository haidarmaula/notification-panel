import { For, Show, createEffect, createSignal, onCleanup } from 'solid-js';
import { A, useNavigate, useParams, useSearchParams } from '@solidjs/router';
import { createQuery, useQueryClient } from '@tanstack/solid-query';
import { ArrowLeft, ArrowRight, Plus, Search, Shield } from 'lucide-solid';
import { data } from '../../lib/api/client';
import { Failure, Loading, Status } from '../../components/ui/Feedback';
import { useWrite, WriteFeedback } from '../management/shared';
import { useUnsaved } from '../../app/unsaved';

type StaffId = number | string;
type StaffMember = {
  id: StaffId;
  role_id: number;
  role_name: StaffRole;
  name: string;
  email: string;
  is_active: boolean;
  created_at: string;
  updated_at: string;
};
type StaffRole = 'SUPER_ADMIN' | 'ADMIN' | 'OPERATOR' | 'VIEWER';
type StaffPage = {
  data: StaffMember[];
  total: number;
  page: number;
  limit: number;
  total_pages: number;
};
type Envelope<T> = { data: T };
const roles: StaffRole[] = ['SUPER_ADMIN', 'ADMIN', 'OPERATOR', 'VIEWER'];
const staffIdPattern = /^[1-9][0-9]{0,18}$/;
const maxStaffId = 9223372036854775807n;
function isStaffId(value: string) {
  return staffIdPattern.test(value) && BigInt(value) <= maxStaffId;
}
function passwordBytes(value: string) {
  return new TextEncoder().encode(value).length;
}

export default function Staff() {
  const [params, setParams] = useSearchParams();
  const initialSearch = typeof params.search === 'string' ? params.search.slice(0, 255) : '';
  const [term, setTerm] = createSignal(initialSearch);
  const pageNumber = () => {
    const value = typeof params.page === 'string' ? Number(params.page) : 1;
    return Number.isSafeInteger(value) && value > 0 ? value : 1;
  };
  const search = () => (typeof params.search === 'string' ? params.search : '');
  const page = createQuery(() => ({
    queryKey: ['admin-staff', search(), pageNumber()],
    queryFn: ({ signal }) =>
      data<StaffPage>(
        `/staff?page=${pageNumber()}&limit=20${search() ? `&search=${encodeURIComponent(search())}` : ''}`,
        signal,
      ),
  }));
  const profile = createQuery(() => ({
    queryKey: ['profile'],
    queryFn: ({ signal }) => data<{ id: StaffId }>('/profile', signal),
  }));
  const operation = useWrite();
  const client = useQueryClient();
  const profileKnown = () => profile.isSuccess && profile.data?.id !== undefined;
  const isCurrentStaff = (staff: StaffMember) =>
    profileKnown() && String(profile.data!.id) === String(staff.id);
  function applySearch(event: SubmitEvent) {
    event.preventDefault();
    const next = term().trim().slice(0, 255);
    setParams({ search: next || undefined, page: undefined });
  }
  async function toggleActive(staff: StaffMember) {
    if (!profileKnown() || isCurrentStaff(staff) || operation.blocked()) return;
    const result = await operation.write<Envelope<StaffMember>>(
      `/staff/${String(staff.id)}/status`,
      'PATCH',
      {
        is_active: !staff.is_active,
      },
    );
    if (result) await client.invalidateQueries({ queryKey: ['admin-staff'] });
  }
  return (
    <>
      <header class="page-heading">
        <div>
          <h1>Kelola staf</h1>
          <p class="muted">Atur akun operator yang dapat mengakses dashboard.</p>
        </div>
        <A class="button primary" href="/apps/admin/staff/new">
          <Plus size={18} />
          Tambah staf
        </A>
      </header>
      <section class="surface stack">
        <div class="section-heading">
          <div>
            <h2>Direktori staf</h2>
            <p class="muted">Cari berdasarkan nama atau email.</p>
          </div>
        </div>
        <form class="campaign-lookup" onSubmit={applySearch} role="search">
          <label for="staff-search">Cari staf</label>
          <div class="actions">
            <input
              id="staff-search"
              type="search"
              maxlength="255"
              value={term()}
              onInput={(event) => setTerm(event.currentTarget.value)}
              placeholder="Nama atau email"
            />
            <button class="button secondary" type="submit">
              <Search size={16} />
              Cari
            </button>
          </div>
        </form>
        <WriteFeedback operation={operation} />
        <div aria-busy={page.isFetching}>
          <Show when={!page.isPending} fallback={<Loading rows={5} />}>
            <Show
              when={!page.isError}
              fallback={<Failure error={page.error} retry={() => void page.refetch()} />}
            >
              <Show
                when={page.data?.data.length}
                fallback={
                  <div class="empty-state">
                    <span class="empty-icon">
                      <Shield size={26} />
                    </span>
                    <h2>{search() ? 'Tidak ada staf yang cocok.' : 'Belum ada akun staf.'}</h2>
                    <p>
                      {search()
                        ? 'Coba kata pencarian lain.'
                        : 'Buat akun staf untuk memberikan akses dashboard.'}
                    </p>
                    <Show when={!search()}>
                      <A class="button primary" href="/apps/admin/staff/new">
                        <Plus size={16} />
                        Tambah staf pertama
                      </A>
                    </Show>
                  </div>
                }
              >
                <div class="table-wrap">
                  <table class="directory-table admin-staff-table">
                    <thead>
                      <tr>
                        <th>Nama staf</th>
                        <th>Peran</th>
                        <th>Status</th>
                        <th>
                          <span class="sr-only">Tindakan</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      <For each={page.data?.data}>
                        {(staff) => (
                          <tr>
                            <td>
                              <A
                                class="campaign-title"
                                href={`/apps/admin/staff/${String(staff.id)}`}
                              >
                                {staff.name}
                              </A>
                              <span class="row-meta">
                                {staff.email} · #{staff.id}
                              </span>
                            </td>
                            <td>
                              <span class="mono">{staff.role_name}</span>
                            </td>
                            <td>
                              <Status value={staff.is_active ? 'ACTIVE' : 'INACTIVE'} />
                            </td>
                            <td>
                              <button
                                class="row-action"
                                type="button"
                                disabled={
                                  operation.blocked() || !profileKnown() || isCurrentStaff(staff)
                                }
                                aria-label={`${staff.is_active ? 'Nonaktifkan' : 'Aktifkan'} akun ${staff.name}${isCurrentStaff(staff) ? ', akun Anda' : !profileKnown() ? ', identitas belum diverifikasi' : ''}`}
                                onClick={() => void toggleActive(staff)}
                              >
                                {staff.is_active ? 'Nonaktifkan' : 'Aktifkan'}
                              </button>
                            </td>
                          </tr>
                        )}
                      </For>
                    </tbody>
                  </table>
                </div>
                <div class="pagination">
                  <span>
                    {page.data?.total ?? 0} akun · Halaman {pageNumber()} dari{' '}
                    {Math.max(page.data?.total_pages ?? 0, 1)}
                  </span>
                  <div>
                    <button
                      class="button secondary small"
                      disabled={pageNumber() <= 1 || page.isFetching}
                      onClick={() =>
                        setParams({ page: pageNumber() > 2 ? String(pageNumber() - 1) : undefined })
                      }
                    >
                      Sebelumnya
                    </button>
                    <button
                      class="button secondary small"
                      disabled={pageNumber() >= (page.data?.total_pages ?? 0) || page.isFetching}
                      onClick={() => setParams({ page: String(pageNumber() + 1) })}
                    >
                      Berikutnya <ArrowRight size={15} />
                    </button>
                  </div>
                </div>
              </Show>
            </Show>
          </Show>
        </div>
      </section>
    </>
  );
}

export function StaffEditor() {
  const params = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const operation = useWrite();
  const passwordOperation = useWrite();
  const setDirty = useUnsaved();
  const editing = () => !!params.staffId;
  const validId = () => !!params.staffId && isStaffId(params.staffId);
  const [name, setName] = createSignal('');
  const [email, setEmail] = createSignal('');
  const [role, setRole] = createSignal<StaffRole>('OPERATOR');
  const [password, setPassword] = createSignal('');
  const [currentPassword, setCurrentPassword] = createSignal('');
  const [newPassword, setNewPassword] = createSignal('');
  const [profileError, setProfileError] = createSignal('');
  const [passwordValidationError, setPasswordValidationError] = createSignal('');
  const [resetSuccess, setResetSuccess] = createSignal(false);
  const profile = createQuery(() => ({
    queryKey: ['profile'],
    queryFn: ({ signal }) => data<{ id: StaffId; role_name: string }>('/profile', signal),
  }));
  const profileKnown = () => profile.isSuccess && profile.data?.id !== undefined;
  const staff = createQuery(() => ({
    queryKey: ['admin-staff-detail', params.staffId],
    queryFn: ({ signal }) => data<StaffMember>(`/staff/${params.staffId}`, signal),
    enabled: editing() && validId(),
  }));
  let loadedId = '';
  createEffect(() => {
    if (staff.data && loadedId !== params.staffId) {
      loadedId = params.staffId ?? '';
      setName(staff.data.name);
      setEmail(staff.data.email);
      setRole(staff.data.role_name);
    }
  });
  onCleanup(() => {
    setPassword('');
    setCurrentPassword('');
    setNewPassword('');
  });
  const self = () =>
    editing() &&
    profileKnown() &&
    staff.data?.id !== undefined &&
    String(profile.data!.id) === String(staff.data.id);
  const selfSensitiveBlocked = () => editing() && (!profileKnown() || self());
  function changed() {
    setDirty(true);
    setProfileError('');
  }
  async function save(event: SubmitEvent) {
    event.preventDefault();
    setProfileError('');
    if (operation.blocked()) return;
    const body = editing()
      ? {
          role: profileKnown() && !self() && role() !== staff.data?.role_name ? role() : '',
          name: name().trim(),
          email: email().trim(),
        }
      : { role: role(), name: name().trim(), email: email().trim(), password: password() };
    if (!body.name || !body.email || (!editing() && !body.password)) {
      setProfileError('Nama, email, dan kata sandi wajib diisi.');
      return;
    }
    if (!editing()) {
      const size = passwordBytes(password());
      if (size < 8 || size > 72) {
        setProfileError('Kata sandi awal harus berukuran 8–72 byte.');
        return;
      }
    }
    const result = await operation.write<Envelope<StaffMember>>(
      editing() ? `/staff/${params.staffId}` : '/staff',
      editing() ? 'PATCH' : 'POST',
      body,
    );
    if (result) {
      setDirty(false);
      setPassword('');
      await client.invalidateQueries({ queryKey: ['admin-staff'] });
      if (editing())
        await client.invalidateQueries({ queryKey: ['admin-staff-detail', params.staffId] });
      if (self()) await client.invalidateQueries({ queryKey: ['profile'] });
      navigate(`/apps/admin/staff/${result.data.id}`);
    }
  }
  async function resetTargetPassword(event: SubmitEvent) {
    event.preventDefault();
    if (!staff.data || !profileKnown() || self() || passwordOperation.blocked()) return;
    setResetSuccess(false);
    setPasswordValidationError('');
    if (!currentPassword()) {
      setPasswordValidationError('Masukkan kata sandi Anda saat ini.');
      return;
    }
    const size = passwordBytes(newPassword());
    if (size < 8 || size > 72) {
      setPasswordValidationError('Kata sandi baru harus berukuran 8–72 byte.');
      return;
    }
    const result = await passwordOperation.write<Envelope<null>>(
      `/staff/${params.staffId}/password`,
      'PATCH',
      {
        current_password: currentPassword(),
        password: newPassword(),
      },
    );
    if (result) {
      setCurrentPassword('');
      setNewPassword('');
      setResetSuccess(true);
    }
  }
  async function remove() {
    if (!staff.data || !profileKnown() || self() || operation.blocked()) return;
    const confirmed = window.confirm(
      `Hapus akun ${staff.data.name} (${staff.data.email})? Tindakan ini tidak dapat dibatalkan.`,
    );
    if (!confirmed) return;
    const result = await operation.write<Envelope<null>>(`/staff/${params.staffId}`, 'DELETE');
    if (result) {
      setDirty(false);
      await client.invalidateQueries({ queryKey: ['admin-staff'] });
      navigate('/apps/admin/staff');
    }
  }
  async function toggleStatus() {
    if (!staff.data || !profileKnown() || self() || operation.blocked()) return;
    const result = await operation.write<Envelope<StaffMember>>(
      `/staff/${params.staffId}/status`,
      'PATCH',
      { is_active: !staff.data.is_active },
    );
    if (result) {
      await client.invalidateQueries({ queryKey: ['admin-staff-detail', params.staffId] });
      await client.invalidateQueries({ queryKey: ['admin-staff'] });
    }
  }
  return (
    <>
      <A class="back-link" href="/apps/admin/staff">
        <ArrowLeft size={16} />
        Semua staf
      </A>
      <header class="page-heading">
        <div>
          <h1>{editing() ? 'Edit staf' : 'Tambah staf'}</h1>
          <p class="muted">
            {editing()
              ? 'Perbarui detail dan akses akun staf.'
              : 'Buat akun untuk anggota tim administrasi.'}
          </p>
        </div>
      </header>
      <Show
        when={!editing() || validId()}
        fallback={
          <div class="notice notice-error" role="alert">
            ID akun staf tidak valid.
          </div>
        }
      >
        <Show when={!editing() || !staff.isPending} fallback={<Loading />}>
          <Show
            when={!editing() || !staff.isError}
            fallback={<Failure error={staff.error} retry={() => void staff.refetch()} />}
          >
            <div class="settings-grid">
              <section class="surface stack">
                <h2>Informasi akun</h2>
                <form class="stack" onInput={changed} onSubmit={(event) => void save(event)}>
                  <fieldset class="stack" disabled={operation.blocked()}>
                    <label for="staff-name">Nama</label>
                    <input
                      id="staff-name"
                      value={name()}
                      maxlength="255"
                      required
                      onInput={(event) => setName(event.currentTarget.value)}
                    />
                    <label for="staff-email">Email masuk</label>
                    <input
                      id="staff-email"
                      value={email()}
                      type="email"
                      maxlength="255"
                      required
                      onInput={(event) => setEmail(event.currentTarget.value)}
                    />
                    <label for="staff-role">Peran</label>
                    <select
                      id="staff-role"
                      value={role()}
                      disabled={selfSensitiveBlocked()}
                      onChange={(event) => setRole(event.currentTarget.value as StaffRole)}
                    >
                      <For each={roles}>{(choice) => <option value={choice}>{choice}</option>}</For>
                    </select>
                    <Show when={self()}>
                      <p class="help-text">Peran akun Anda tidak dapat diubah dari halaman ini.</p>
                    </Show>
                    <Show when={editing() && !profileKnown()}>
                      <p class="help-text" role="status">
                        Identitas Anda belum dapat diverifikasi. Perubahan peran dan tindakan
                        sensitif dinonaktifkan.
                      </p>
                    </Show>
                    <Show when={!editing()}>
                      <label for="staff-create-password">Kata sandi awal</label>
                      <input
                        id="staff-create-password"
                        type="password"
                        autocomplete="new-password"
                        required
                        value={password()}
                        onInput={(event) => setPassword(event.currentTarget.value)}
                      />
                      <p class="help-text">
                        Gunakan kata sandi sepanjang 8–72 byte. Kata sandi tidak akan ditampilkan
                        kembali.
                      </p>
                    </Show>
                    <Show when={profileError()}>
                      <p class="field-error" role="alert">
                        {profileError()}
                      </p>
                    </Show>
                    <WriteFeedback operation={operation} />
                    <button class="button primary" type="submit">
                      {operation.pending()
                        ? 'Menyimpan…'
                        : editing()
                          ? 'Simpan perubahan'
                          : 'Buat akun staf'}
                    </button>
                  </fieldset>
                </form>
              </section>
              <Show when={editing() && staff.data}>
                <section class="surface stack">
                  <h2>Akses akun</h2>
                  <p class="muted">Status masuk untuk {staff.data?.email}.</p>
                  <p>
                    <Status value={staff.data?.is_active ? 'ACTIVE' : 'INACTIVE'} />
                  </p>
                  <button
                    class="button secondary"
                    disabled={operation.blocked() || selfSensitiveBlocked()}
                    onClick={() => void toggleStatus()}
                  >
                    {staff.data?.is_active ? 'Nonaktifkan akun' : 'Aktifkan akun'}
                  </button>
                  <Show when={self()}>
                    <p class="help-text">
                      Akun yang sedang digunakan tidak dapat dinonaktifkan di sini.
                    </p>
                  </Show>
                </section>
                <section class="surface stack">
                  <h2>Atur kata sandi staf</h2>
                  <p class="muted">
                    Konfirmasi dengan kata sandi Anda sebelum menetapkan kata sandi baru.
                  </p>
                  <Show when={profileKnown() && !self()}>
                    <form class="stack" onSubmit={(event) => void resetTargetPassword(event)}>
                      <fieldset class="stack" disabled={passwordOperation.blocked()}>
                        <label for="admin-current-password">Kata sandi Anda saat ini</label>
                        <input
                          id="admin-current-password"
                          type="password"
                          autocomplete="current-password"
                          required
                          value={currentPassword()}
                          onInput={(event) => {
                            setCurrentPassword(event.currentTarget.value);
                            setResetSuccess(false);
                          }}
                        />
                        <label for="staff-new-password">Kata sandi baru staf</label>
                        <input
                          id="staff-new-password"
                          type="password"
                          autocomplete="new-password"
                          required
                          value={newPassword()}
                          onInput={(event) => {
                            setNewPassword(event.currentTarget.value);
                            setResetSuccess(false);
                          }}
                        />
                        <p class="help-text">
                          Gunakan kata sandi sepanjang 8–72 byte. Perubahan berlaku saat staf masuk
                          berikutnya.
                        </p>
                        <Show when={passwordValidationError()}>
                          <p class="field-error" role="alert">
                            {passwordValidationError()}
                          </p>
                        </Show>
                        <WriteFeedback operation={passwordOperation} />
                        <Show when={resetSuccess()}>
                          <p class="notice notice-success" role="status">
                            Kata sandi staf berhasil diperbarui.
                          </p>
                        </Show>
                        <button class="button secondary" type="submit">
                          {passwordOperation.pending() ? 'Menyimpan…' : 'Atur kata sandi'}
                        </button>
                      </fieldset>
                    </form>
                  </Show>
                  <Show when={self()}>
                    <p class="help-text">
                      Untuk mengubah kata sandi sendiri, buka{' '}
                      <A href="/apps/settings">pengaturan akun</A>. Anda akan diminta masuk kembali
                      setelah perubahan.
                    </p>
                  </Show>
                </section>
                <section class="surface stack">
                  <h2>Hapus akun</h2>
                  <p class="muted">Akun staf yang dihapus tidak dapat dipulihkan.</p>
                  <WriteFeedback operation={operation} />
                  <button
                    class="button danger"
                    disabled={operation.blocked() || selfSensitiveBlocked()}
                    onClick={() => void remove()}
                  >
                    Hapus akun staf
                  </button>
                  <Show when={self()}>
                    <p class="help-text">Akun yang sedang digunakan tidak dapat dihapus.</p>
                  </Show>
                </section>
              </Show>
            </div>
          </Show>
        </Show>
      </Show>
    </>
  );
}
