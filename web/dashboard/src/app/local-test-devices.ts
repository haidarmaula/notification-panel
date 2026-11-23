const localAndroidApp = '10000000-0000-4000-8000-000000000003';

export const localTestDevicesPath = `/apps/${localAndroidApp}/test-devices`;

export function localTestDevicesEnabled() {
  return import.meta.env.VITE_HELLO_REAL_FCM === '1' && window.location.host === 'localhost:5273';
}

export function isLocalTestDevicesPath(path: unknown): boolean {
  return localTestDevicesEnabled() && path === localTestDevicesPath;
}
