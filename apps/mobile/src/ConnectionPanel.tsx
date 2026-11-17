import { Modal, Pressable, ScrollView, StyleSheet, Text, TextInput } from 'react-native';

type Props = {
  visible: boolean; gatewayUrl: string; sessionToken: string;
  onGateway: (value: string) => void; onToken: (value: string) => void;
  onClose: () => void; onClear: () => void;
};

// Outside the role/session subtree: typing does not lose keyboard focus when
// private role data is discarded. Tokens are never persisted or placed in keys.
export function ConnectionPanel(props: Props) {
  return <Modal visible={props.visible} animationType="slide" onRequestClose={props.onClose}>
    <ScrollView contentContainerStyle={styles.panel} keyboardShouldPersistTaps="handled" accessibilityViewIsModal>
      <Text accessibilityRole="header" style={styles.heading}>Gateway settings</Text>
      <Text style={styles.copy}>Use the public HTTPS identity gateway, never a backend service token. Manual session entry is not integrated sign-in. Editing settings cancels pending requests and clears private role data.</Text>
      <Text style={styles.copy}>Public gateway URL</Text>
      <TextInput accessibilityLabel="Public gateway URL" autoCapitalize="none" autoCorrect={false} keyboardType="url" value={props.gatewayUrl} onChangeText={props.onGateway} style={styles.input} placeholder="Public HTTPS gateway" />
      <Text style={styles.copy}>User session token · memory only</Text>
      <TextInput accessibilityLabel="User session token" autoCapitalize="none" autoCorrect={false} autoComplete="off" secureTextEntry value={props.sessionToken} onChangeText={props.onToken} style={styles.input} />
      <Text style={styles.copy}>Role selection changes the workspace, not server permissions. HTTP is development-only on localhost or the Android emulator loopback; a real device needs trusted HTTPS, not a LAN HTTP address.</Text>
      <Pressable accessibilityRole="button" onPress={props.onClear} style={styles.button}><Text style={styles.buttonText}>Clear session and private records</Text></Pressable>
      <Pressable accessibilityRole="button" onPress={props.onClose} style={styles.button}><Text style={styles.buttonText}>Done</Text></Pressable>
    </ScrollView>
  </Modal>;
}
const styles = StyleSheet.create({
  panel: { padding: 24, paddingTop: 64, gap: 12, maxWidth: 760, width: '100%', alignSelf: 'center' },
  heading: { fontSize: 24, fontWeight: '700', color: '#19352f' },
  copy: { color: '#43564e', fontSize: 15, lineHeight: 23 },
  input: { borderWidth: 1, borderColor: '#63776d', borderRadius: 10, padding: 14, minHeight: 48, color: '#19352f' },
  button: { minHeight: 48, padding: 14, backgroundColor: '#19352f', borderRadius: 10 },
  buttonText: { color: '#ffffff', fontWeight: '700', textAlign: 'center' },
});