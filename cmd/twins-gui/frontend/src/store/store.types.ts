import type { WalletSlice } from './slices/walletSlice';
import type { MasternodeSlice } from './slices/masternodeSlice';
import type { AppSlice } from './slices/appSlice';
import type { SendSlice } from './slices/sendSlice';
import type { CoinControlSlice } from './slices/coinControlSlice';
import type { ReceiveSlice } from './slices/receiveSlice';
import type { ReceivingAddressesSlice } from './slices/receivingAddressesSlice';
import type { TransactionsSlice } from './slices/transactionsSlice';
import type { ExplorerSlice } from './slices/explorerSlice';
import type { OptionsSlice } from './slices/optionsSlice';
import type { ToolsSlice } from './slices/toolsSlice';
import type { SignVerifySlice } from './slices/signVerifySlice';
import type { AddressBookSlice } from './slices/addressBookSlice';
import type { StateCreator } from 'zustand';

// Combined store type used by all slices for Zustand v5 compatibility.
// Zustand v5 requires each slice's StateCreator to know the full store type
// so the `set` function overloads match correctly.
export type StoreState = WalletSlice &
  MasternodeSlice &
  AppSlice &
  SendSlice &
  CoinControlSlice &
  ReceiveSlice &
  ReceivingAddressesSlice &
  TransactionsSlice &
  ExplorerSlice &
  OptionsSlice &
  ToolsSlice &
  SignVerifySlice &
  AddressBookSlice;

// Zustand v5 middleware mutators declaration for immer middleware.
// All slices use this to declare that the immer middleware wraps the store.
type ImmerMutators = [['zustand/immer', never]];

// Convenience type for slice creators with immer middleware.
// Usage: SliceCreator<MySlice> instead of StateCreator<StoreState, [['zustand/immer', never]], [], MySlice>
export type SliceCreator<T> = StateCreator<StoreState, ImmerMutators, [], T>;
