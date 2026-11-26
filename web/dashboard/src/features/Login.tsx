import { useRef, useState, type FormEvent } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { Card } from '@astryxdesign/core/Card';
import { Center } from '@astryxdesign/core/Center';
import { LayoutContent } from '@astryxdesign/core/Layout';
import { Grid } from '@astryxdesign/core/Grid';
import { AspectRatio } from '@astryxdesign/core/AspectRatio';
import { useMediaQuery } from '@astryxdesign/core/hooks';
import { HStack, StackItem, VStack } from '@astryxdesign/core/Stack';
import { Heading, Text } from '@astryxdesign/core/Text';
import { InputGroup, InputGroupText } from '@astryxdesign/core/InputGroup';
import { TextInput } from '@astryxdesign/core/TextInput';
import { Button } from '@astryxdesign/core/Button';
import { IconButton } from '@astryxdesign/core/IconButton';
import { Banner } from '@astryxdesign/core/Banner';
import { Icon } from '@astryxdesign/core/Icon';
import { BellIcon, EyeIcon, EyeSlashIcon } from '@heroicons/react/24/outline';
import autumn600 from '../assets/autumn-600.webp';
import autumn1000 from '../assets/autumn-1000.webp';
import { useAuth } from '../app/auth-context';
import { isLocalTestDevicesPath, localTestDevicesPath } from '../app/local-test-devices';
import { ApiError } from '../lib/api/client';
import { Failure, Loading } from '../components/ui/Feedback';
import { usePageFocus } from '../components/ui/usePageFocus';

export default function Login() {
  usePageFocus();
  const isMobile = useMediaQuery('(width < 768px)');
  const auth = useAuth();
  const location = useLocation();
  const navigate = useNavigate();
  const destination = isLocalTestDevicesPath(location.state?.returnTo)
    ? localTestDevicesPath
    : '/apps';
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const inFlight = useRef(false);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (inFlight.current || !auth.csrf()) return;
    inFlight.current = true;
    setPending(true);
    setError('');
    try {
      await auth.login(email, password);
      setPassword('');
      navigate(destination, { replace: true });
    } catch (cause) {
      setError(
        cause instanceof ApiError && cause.status === 401
          ? 'Email or password is incorrect.'
          : 'Unable to sign in. Check your connection and try again.',
      );
    } finally {
      inFlight.current = false;
      setPending(false);
    }
  }
  if (auth.session.isPending) return <Loading />;
  if (auth.authenticated()) return <Navigate to={destination} replace />;
  return (
    <LayoutContent role="main">
      <Center minHeight="100dvh" padding={6}>
        <VStack width="100%" maxWidth={1000}>
          <Card padding={isMobile ? 6 : 8} width="100%">
            <Grid columns={isMobile ? 1 : 2} gap={8} align="stretch">
              <VStack gap={8} height="100%">
                <HStack gap={2} vAlign="center">
                  <Icon icon={BellIcon} size="lg" />
                  <Text weight="bold" size="lg">
                    hello.
                  </Text>
                </HStack>
                <StackItem size="fill">
                  <Center axis="vertical" height="100%">
                    <VStack gap={4} width="100%">
                      <VStack gap={1}>
                        <Heading level={2} accessibilityLevel={1}>
                          Welcome back
                        </Heading>
                        <Text color="secondary" size="sm">
                          Sign in to your notification workspace.
                        </Text>
                      </VStack>
                      {auth.session.isError && (
                        <Failure
                          error={auth.session.error}
                          retry={() => void auth.session.refetch()}
                        />
                      )}
                      <VStack as="form" gap={4} onSubmit={(event) => void submit(event)}>
                        <TextInput
                          label="Email"
                          size="lg"
                          placeholder="you@example.com"
                          type="email"
                          htmlName="email"
                          autoComplete="username"
                          value={email}
                          onChange={setEmail}
                          isRequired
                          isDisabled={pending}
                        />
                        <InputGroup label="Password" size="lg" isRequired isDisabled={pending}>
                          <TextInput
                            label="Password"
                            isLabelHidden
                            size="lg"
                            placeholder="Enter your password"
                            type={showPassword ? 'text' : 'password'}
                            htmlName="password"
                            autoComplete="current-password"
                            value={password}
                            onChange={setPassword}
                            isRequired
                            isDisabled={pending}
                          />
                          <InputGroupText>
                            <IconButton
                              label={showPassword ? 'Hide password' : 'Show password'}
                              icon={<Icon icon={showPassword ? EyeSlashIcon : EyeIcon} size="sm" />}
                              variant="ghost"
                              size="lg"
                              isDisabled={pending}
                              onClick={() => setShowPassword((value) => !value)}
                            />
                          </InputGroupText>
                        </InputGroup>
                        {error && (
                          <Banner
                            status="error"
                            role="alert"
                            title="Sign-in failed"
                            description={error}
                          />
                        )}
                        <Button
                          label={pending ? 'Signing in…' : 'Sign in'}
                          type="submit"
                          variant="primary"
                          size="lg"
                          isDisabled={pending || !auth.csrf()}
                          isLoading={pending}
                        />
                      </VStack>
                    </VStack>
                  </Center>
                </StackItem>
                <Text type="supporting" color="secondary">
                  Need access? Contact your administrator.
                </Text>
              </VStack>
              {!isMobile && (
                <AspectRatio ratio={3 / 4} fit="cover">
                  <img
                    src={autumn600}
                    srcSet={`${autumn600} 600w, ${autumn1000} 1000w`}
                    sizes="(min-width: 1048px) 452px, calc(50vw - 72px)"
                    alt=""
                    width={600}
                    height={900}
                  />
                </AspectRatio>
              )}
            </Grid>
          </Card>
        </VStack>
      </Center>
    </LayoutContent>
  );
}
