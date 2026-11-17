import { Pressable, StyleSheet, Text, TextInput, View } from 'react-native';

export function Field({ label, value, onChangeText, keyboardType = 'default', editable = true, secure = false }: {
  label: string; value: string; onChangeText: (value: string) => void;
  keyboardType?: 'default' | 'decimal-pad'; editable?: boolean; secure?: boolean;
}) {
  return <View style={styles.field}>
    <Text style={styles.label}>{label}</Text>
    <TextInput accessibilityLabel={label} autoCapitalize="none" autoCorrect={false} editable={editable}
      keyboardType={keyboardType} onChangeText={onChangeText} placeholder={label} placeholderTextColor="#64756e"
      secureTextEntry={secure} style={styles.input} value={value} />
  </View>;
}

export function ActionButton({ label, onPress, disabled = false, secondary = false }: {
  label: string; onPress: () => void; disabled?: boolean; secondary?: boolean;
}) {
  return <Pressable accessibilityRole="button" disabled={disabled} accessibilityState={{ disabled }} onPress={onPress}
    style={[styles.button, secondary && styles.secondary, disabled && styles.disabled]}>
    <Text style={[styles.buttonText, secondary && styles.secondaryText]}>{label}</Text>
  </Pressable>;
}

export function StateCard({ title, children }: { title: string; children: string }) {
  return <View style={styles.state}>
    <Text accessibilityRole="header" style={styles.stateTitle}>{title}</Text>
    <Text style={styles.copy}>{children}</Text>
  </View>;
}

const styles = StyleSheet.create({
  field: { flex: 1, minWidth: 125, marginTop: 10 },
  label: { color: '#52645e', fontSize: 12, fontWeight: '700', marginBottom: 6 },
  input: { backgroundColor: '#f8f9f7', borderColor: '#d5dfd8', borderWidth: 1, borderRadius: 10, color: '#203b34', fontSize: 14, paddingHorizontal: 12, paddingVertical: 12, minHeight: 48 },
  button: { backgroundColor: '#ad4020', minHeight: 48, justifyContent: 'center', alignItems: 'center', borderRadius: 11, padding: 12, marginTop: 13 },
  secondary: { backgroundColor: '#e9f0ec' }, disabled: { opacity: 0.48 },
  buttonText: { color: '#fff', fontWeight: '800', fontSize: 13, textAlign: 'center' }, secondaryText: { color: '#24443b' },
  state: { backgroundColor: '#f0f5f1', padding: 16, borderRadius: 12, marginVertical: 12 },
  stateTitle: { color: '#19352f', fontSize: 15, fontWeight: '800' },
  copy: { color: '#52645b', fontSize: 13, lineHeight: 20, marginTop: 6 },
});