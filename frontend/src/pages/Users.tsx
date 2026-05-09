import { useEffect, useState, FormEvent } from 'react';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { useAuth } from '@/components/AuthProvider';
import { Modal, Field, SelectField, Button, Stack } from '@/components/ui';
import { api, type UserRow } from '@/lib/api';
import { tsLong } from '@/lib/utils';

export default function UsersPage() {
  const { user: me } = useAuth();
  const [users, setUsers] = useState<UserRow[] | null | undefined>(undefined);
  const [showNew, setShowNew] = useState(false);
  const [resetFor, setResetFor] = useState<UserRow | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const refresh = () => {
    setUsers(undefined);
    api.listUsers().then(u => setUsers(u ?? [])).catch(() => setUsers(null));
  };
  useEffect(refresh, []);

  // Admin gate: if a viewer somehow lands here, surface a clear message
  // rather than a stack of 401s from listUsers.
  if (me && me.role !== 'admin') {
    return (
      <>
        <Topbar title="Users" />
        <div id="content">
          <Empty icon="🔒" title="Admin access required">
            User management is only available to administrators.
          </Empty>
        </div>
      </>
    );
  }

  const onDelete = async (u: UserRow) => {
    setActionError(null);
    if (!confirm(`Disable user ${u.email}? They will no longer be able to sign in.`)) return;
    try {
      await api.deleteUser(u.id);
      refresh();
    } catch (e) {
      setActionError(humanize(e));
    }
  };

  return (
    <>
      <Topbar title="Users" />
      <div id="content">
        <div className="controls">
          <button onClick={() => setShowNew(true)}>+ New user</button>
          <span style={{ color: 'var(--text3)', fontSize: 12, marginLeft: 'auto' }}>
            {users?.length ?? 0} users
          </span>
        </div>

        {actionError && (
          <div style={{
            color: 'var(--err)', fontSize: 13, marginBottom: 10,
            padding: '6px 10px', background: 'rgba(220,38,38,0.08)',
            border: '1px solid rgba(220,38,38,0.3)', borderRadius: 4,
          }}>
            {actionError}
          </div>
        )}

        {users === undefined && <Spinner />}
        {users !== undefined && (!users || users.length === 0) && (
          <Empty icon="◯" title="No users yet">
            Create the first user to get started.
          </Empty>
        )}
        {users && users.length > 0 && (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Email</th>
                  <th>Role</th>
                  <th>Provider</th>
                  <th>Created</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {users.map(u => {
                  const isMe = me?.id === u.id;
                  const isOIDC = u.authProvider === 'oidc';
                  return (
                    <tr key={u.id}>
                      <td>
                        <span style={{ fontWeight: 600 }}>{u.email}</span>
                        {isMe && (
                          <span style={{
                            marginLeft: 8, fontSize: 10, color: 'var(--text3)',
                            border: '1px solid var(--border)', borderRadius: 3,
                            padding: '1px 5px', textTransform: 'uppercase',
                          }}>you</span>
                        )}
                      </td>
                      <td>
                        <span className={`badge ${u.role === 'admin' ? 'b-info' : u.role === 'editor' ? 'b-warn' : 'b-ok'}`}>
                          {u.role}
                        </span>
                      </td>
                      <td>
                        <span style={{
                          fontSize: 10, color: 'var(--text3)',
                          border: '1px solid var(--border)', borderRadius: 3,
                          padding: '1px 6px', textTransform: 'uppercase',
                        }}>{u.authProvider || 'local'}</span>
                      </td>
                      <td className="mono" style={{ color: 'var(--text3)' }}>
                        {tsLong(u.createdAt)}
                      </td>
                      <td style={{ textAlign: 'right' }}>
                        <button className="sec" onClick={() => setResetFor(u)}
                          disabled={isOIDC}
                          title={isOIDC ? 'OIDC users authenticate via SSO — no local password' : 'Set a new password'}
                          style={{ marginRight: 6 }}>
                          Reset password
                        </button>
                        <button className="sec" onClick={() => onDelete(u)}
                          disabled={isMe}
                          title={isMe ? "You can't delete your own account" : 'Disable user'}>
                          Delete
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {showNew && (
          <NewUserModal
            onClose={() => setShowNew(false)}
            onCreated={() => { setShowNew(false); refresh(); }}
          />
        )}
        {resetFor && (
          <ResetPasswordModal
            user={resetFor}
            onClose={() => setResetFor(null)}
            onDone={() => { setResetFor(null); refresh(); }}
          />
        )}
      </div>
    </>
  );
}

function NewUserModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<'admin' | 'editor' | 'viewer'>('viewer');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      await api.createUser(email.trim(), password, role);
      onCreated();
    } catch (err) {
      setError(humanize(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={true}
      onClose={onClose}
      title="New user"
      size="sm"
      initialFocus="input[type=email]"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button type="submit" form="new-user-form" loading={busy}>Create</Button>
        </>
      }>
      <form id="new-user-form" onSubmit={submit}>
        <Stack gap={3}>
          <Field
            label="Email"
            type="email"
            required
            value={email}
            onChange={e => setEmail(e.target.value)} />
          <Field
            label="Password"
            hint="At least 6 characters."
            type="password"
            required
            minLength={6}
            value={password}
            onChange={e => setPassword(e.target.value)} />
          <SelectField
            label="Role"
            value={role}
            onChange={e => setRole(e.target.value as 'admin' | 'editor' | 'viewer')}>
            <option value="viewer">Viewer (read only)</option>
            <option value="editor">Editor (dashboards / monitors / alerts)</option>
            <option value="admin">Admin (full access)</option>
          </SelectField>
          {error && <ErrorBox>{error}</ErrorBox>}
        </Stack>
      </form>
    </Modal>
  );
}

function ResetPasswordModal({ user, onClose, onDone }: {
  user: UserRow; onClose: () => void; onDone: () => void;
}) {
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      await api.resetUserPassword(user.id, password);
      onDone();
    } catch (err) {
      setError(humanize(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={true}
      onClose={onClose}
      title={`Reset password — ${user.email}`}
      size="sm"
      initialFocus="input[type=password]"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button type="submit" form="reset-pw-form" loading={busy}>Set password</Button>
        </>
      }>
      <form id="reset-pw-form" onSubmit={submit}>
        <Stack gap={3}>
          <Field
            label="New password"
            hint="At least 6 characters."
            type="password"
            required
            minLength={6}
            value={password}
            onChange={e => setPassword(e.target.value)} />
          {error && <ErrorBox>{error}</ErrorBox>}
        </Stack>
      </form>
    </Modal>
  );
}

// ErrorBox is the inline form-level error styling — kept as a
// local helper because it's used in two places in this file and
// the global Field error slot only covers per-field errors. If a
// third caller in another page wants the same look, lift this
// to ui/.
function ErrorBox({ children }: { children: React.ReactNode }) {
  return (
    <div style={{
      color: 'var(--err)', fontSize: 12, marginTop: 6,
      padding: '6px 10px', background: 'rgba(220,38,38,0.08)',
      border: '1px solid rgba(220,38,38,0.3)', borderRadius: 4,
    }}>
      {children}
    </div>
  );
}

function humanize(err: unknown): string {
  const msg = err instanceof Error ? err.message : String(err);
  // Strip "HTTP 4xx: " prefix and try to pull a JSON {"error":"..."} body.
  const body = msg.replace(/^HTTP \d+:\s*/, '');
  try {
    const j = JSON.parse(body);
    if (j && typeof j.error === 'string') return j.error;
  } catch {}
  return body || msg;
}
