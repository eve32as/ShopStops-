// Test-only semantic DOM adapters. Deliberately not a React Native Web implementation.
import { useSyncExternalStore } from 'react';

function semantics(props: any) {
  const state = props.accessibilityState ?? {};
  const role = props.accessibilityRole === 'header' ? 'heading' : props.accessibilityRole;
  return {
    role,
    'aria-label': props.accessibilityLabel,
    'aria-live': props.accessibilityLiveRegion,
    'aria-disabled': state.disabled,
    'aria-expanded': state.expanded,
    'aria-selected': role === 'tab' ? state.selected : undefined,
    'aria-checked': role === 'radio' ? state.selected : undefined,
    'aria-level': role === 'heading' ? 2 : undefined,
  };
}
export function View(props: any) { return <div {...semantics(props)}>{props.children}</div>; }
export function Text(props: any) { return <span {...semantics(props)}>{props.children}</span>; }
export const ScrollView = View;
export const KeyboardAvoidingView = View;
export function Pressable(props: any) {
  return <button type="button" {...semantics(props)} disabled={Boolean(props.disabled || props.accessibilityState?.disabled)} onClick={props.onPress}>{props.children}</button>;
}
export function TextInput(props: any) {
  return <input {...semantics(props)} type={props.secureTextEntry ? 'password' : 'text'}
    value={props.value} placeholder={props.placeholder} readOnly={props.editable === false}
    autoComplete={props.autoComplete ?? 'off'} onChange={event => props.onChangeText?.(event.target.value)} />;
}
export function Modal(props: any) {
  return props.visible ? <div role="dialog" aria-modal="true" aria-label="Gateway settings" onKeyDown={event => { if (event.key === 'Escape') props.onRequestClose?.(); }}>{props.children}</div> : null;
}
export function Image(props: any) { return <img alt={props.accessibilityLabel ?? ''} src={props.source} width="42" height="42" />; }
export function ActivityIndicator(props: any) { return <div role="status" aria-label={props.accessibilityLabel}>Working…</div>; }
export const StyleSheet = { create: <T,>(styles: T): T => styles, flatten: (style: unknown) => style, hairlineWidth: 1 };
export const Platform = { OS: 'ios', select: (values: any) => values.ios ?? values.default };

const appListeners = new Set<(value: string) => void>();
export const AppState = {
  currentState: 'active',
  addEventListener(_event: string, listener: (value: string) => void) {
    appListeners.add(listener);
    return { remove: () => appListeners.delete(listener) };
  },
};
const links: string[] = [];
export const Linking = { async openURL(url: string) { links.push(url); } };
export const nativeBridge = {
  links,
  appState(value: string) { AppState.currentState = value; appListeners.forEach(listener => listener(value)); },
};

type AlertValue = { title: string; message: string; buttons: { text: string; onPress?: () => void }[] };
let alert: AlertValue | null = null;
const alertListeners = new Set<() => void>();
function notify() { alertListeners.forEach(listener => listener()); }
export const Alert = {
  alert(title: string, message: string, buttons: AlertValue['buttons'] = [{ text: 'OK' }]) {
    alert = { title, message, buttons }; notify();
  },
};
export function NativeAlerts() {
  const value = useSyncExternalStore(listener => { alertListeners.add(listener); return () => { alertListeners.delete(listener); }; }, () => alert);
  if (!value) return null;
  return <div role="alertdialog" aria-modal="true" aria-label={value.title}>
    <h2>{value.title}</h2><p>{value.message}</p>
    {value.buttons.map(button => <button key={button.text} onClick={() => { alert = null; notify(); button.onPress?.(); }}>{button.text}</button>)}
  </div>;
}