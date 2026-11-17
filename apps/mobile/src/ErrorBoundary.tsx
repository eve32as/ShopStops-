import { Component, type ReactNode } from 'react';
import { Pressable, Text, View } from 'react-native';

export class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean; revision: number }> {
  state = { failed: false, revision: 0 };
  static getDerivedStateFromError() { return { failed: true }; }
  render() {
    if (this.state.failed) return <View style={{ padding: 28, paddingTop: 72 }}>
      <Text accessibilityRole="alert" style={{ color: '#19352f', fontSize: 22 }}>This workspace could not be displayed.</Text>
      <Text style={{ color: '#43564e', marginVertical: 20 }}>Reset clears the in-memory session. A submitted change may have completed; check its status before trying again.</Text>
      <Pressable accessibilityRole="button" style={{ padding: 16, backgroundColor: '#19352f' }} onPress={() => this.setState(state => ({ failed: false, revision: state.revision + 1 }))}><Text style={{ color: '#fff' }}>Reset workspace and clear session</Text></Pressable>
    </View>;
    return <View key={this.state.revision} style={{ flex: 1 }}>{this.props.children}</View>;
  }
}